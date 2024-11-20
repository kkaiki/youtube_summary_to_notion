import os
import time
import json
import logging
from datetime import datetime, timedelta
from typing import List, Dict
import pytz
from google.oauth2 import service_account
from googleapiclient.discovery import build
from notion_client import Client
import threading
from queue import Queue
from youtube_transcript_api import YouTubeTranscriptApi
from groq import Groq
from dotenv import load_dotenv

# 定数
MAX_DESCRIPTION_LENGTH = 1500
YOUTUBE_SCOPES = [
    "https://www.googleapis.com/auth/youtube.readonly",
    "https://www.googleapis.com/auth/youtube.force-ssl"
]

def lambda_handler(event, context):
    try:
        main()
        return {
            'statusCode': 200,
            'body': json.dumps('Success')
        }
    except Exception as e:
        return {
            'statusCode': 500,
            'body': json.dumps(str(e))
        }

# Groqクライアントの初期化
class VideoInfo:
    def __init__(self, video_id: str, title: str, description: str, published_at: datetime, 
                 channel_title: str, url: str, caption_summary: str = None):
        self.video_id = video_id
        self.title = title
        self.description = description
        self.published_at = published_at
        self.channel_title = channel_title
        self.url = url
        self.caption_summary = caption_summary

# Groqクライアントの初期化
def get_groq_client():
    api_key = os.getenv("GROQ_API_KEY")
    if not api_key:
        raise ValueError("GROQ_API_KEY environment variable is not set")
    return Groq(api_key=api_key)

class CaptionInfo:
    def __init__(self, language: str, text: str, is_automatic: bool):
        self.language = language
        self.text = text
        self.is_automatic = is_automatic

def truncate_description(description: str) -> str:
    """説明文を制限する関数"""
    if len(description) > MAX_DESCRIPTION_LENGTH:
        return description[:MAX_DESCRIPTION_LENGTH-3] + "..."
    return description

def get_service_account_client():
    """サービスアカウント認証クライアントの取得"""
    try:
        credentials = service_account.Credentials.from_service_account_file(
            'service-account.json',
            scopes=YOUTUBE_SCOPES
        )
        return build('youtube', 'v3', credentials=credentials)
    except Exception as e:
        logging.error(f"サービスアカウントクライアントの作成に失敗: {e}")
        raise

def get_captions(video_id: str) -> List[CaptionInfo]:
    """字幕情報の取得"""
    try:
        # 利用可能な字幕リストを取得
        transcript_list = YouTubeTranscriptApi.list_transcripts(video_id)
        captions = []

        # 日本語字幕を優先的に取得
        try:
            transcript = transcript_list.find_transcript(['ja'])
            transcript_data = transcript.fetch()
            
            # 字幕テキストを結合
            caption_text = ""
            for item in transcript_data:
                text = item['text'].replace('\n', ' ')
                caption_text += text + " "

            caption_info = CaptionInfo(
                language="ja",
                text=caption_text,
                is_automatic=transcript.is_generated
            )
            captions.append(caption_info)
            
        except:
            # 日本語字幕が無い場合は英語字幕を取得
            try:
                transcript = transcript_list.find_transcript(['en'])
                transcript_data = transcript.fetch()
                
                caption_text = ""
                for item in transcript_data:
                    text = item['text'].replace('\n', ' ')
                    caption_text += text + " "

                caption_info = CaptionInfo(
                    language="en",
                    text=caption_text,
                    is_automatic=transcript.is_generated
                )
                captions.append(caption_info)
                
            except Exception as e:
                logging.warning(f"英語字幕の取得に失敗: {e}")

        return captions

    except Exception as e:
        logging.warning(f"字幕取得エラー (video_id: {video_id}): {e}")
        return []


def parse_duration(duration: str) -> int:
    """ISO 8601形式の動画時間を秒数に変換"""
    import re
    import isodate
    return int(isodate.parse_duration(duration).total_seconds())

def get_latest_videos(youtube_service, channel_id: str) -> List[VideoInfo]:
    """最新の動画を取得（60秒未満と40分以上の動画を除外）"""
    try:
        channel_response = youtube_service.channels().list(
            part="contentDetails",
            id=channel_id
        ).execute()

        if not channel_response['items']:
            raise ValueError("チャンネルが見つかりません")

        uploads_playlist_id = channel_response['items'][0]['contentDetails']['relatedPlaylists']['uploads']
        
        playlist_response = youtube_service.playlistItems().list(
            part="snippet,contentDetails",
            playlistId=uploads_playlist_id,
            maxResults=50
        ).execute()

        videos = []
        jst = pytz.timezone('Asia/Tokyo')
        now = datetime.now(jst)
        today = now.replace(hour=0, minute=0, second=0, microsecond=0)
        yesterday = today - timedelta(days=1)

        for item in playlist_response['items']:
            snippet = item['snippet']
            video_id = snippet['resourceId']['videoId']
            
            # 動画の詳細情報を取得
            video_response = youtube_service.videos().list(
                part="contentDetails",
                id=video_id
            ).execute()
            
            if not video_response['items']:
                continue
                
            # 動画時間を取得して変換
            duration = video_response['items'][0]['contentDetails']['duration']
            duration_seconds = parse_duration(duration)
            
            # 60秒未満または40分以上の動画をスキップ
            if duration_seconds <= 60 or duration_seconds >= 2400:
                continue

            published_at = datetime.fromisoformat(snippet['publishedAt'].replace('Z', '+00:00'))
            published_at_jst = published_at.astimezone(jst)
            published_date = published_at_jst.replace(hour=0, minute=0, second=0, microsecond=0)

            if published_date in (today, yesterday):
                video = VideoInfo(
                    video_id=video_id,
                    title=snippet['title'],
                    description=truncate_description(snippet['description']),
                    published_at=published_at,
                    channel_title=snippet['channelTitle'],
                    url=f"https://www.youtube.com/watch?v={video_id}"
                )
                videos.append(video)

        return videos

    except Exception as e:
        logging.error(f"動画取得エラー: {e}")
        raise

def split_text(text: str, max_length: int = 2000) -> List[str]:
    """
    テキストを指定された最大長で分割する
    - 文章の途中で分割されないように、文末で区切る
    - 改行や句点で区切られた段落を考慮する
    - 長すぎる文章は強制的に分割する
    """
    if not text:
        return []
    
    result = []
    current_chunk = ""
    
    # 改行で段落に分割
    paragraphs = text.split('\n')
    
    for paragraph in paragraphs:
        if not paragraph.strip():
            continue
            
        # 文章を句点で分割
        sentences = paragraph.split('。')
        
        for sentence in sentences:
            if not sentence.strip():
                continue
                
            # 文が非常に長い場合は強制的に分割
            if len(sentence) > max_length:
                # 現在のチャンクがある場合は追加
                if current_chunk:
                    result.append(current_chunk)
                    current_chunk = ""
                
                # 長い文を強制的に分割
                for i in range(0, len(sentence), max_length):
                    chunk = sentence[i:i + max_length]
                    result.append(chunk)
                continue
            
            # 通常の文処理
            sentence_with_period = sentence + ('。' if sentence != sentences[-1] else '')
            
            # 現在のチャンクに追加可能か確認
            if len(current_chunk + sentence_with_period) <= max_length:
                current_chunk += sentence_with_period
            else:
                # 現在のチャンクを結果に追加
                if current_chunk:
                    result.append(current_chunk)
                current_chunk = sentence_with_period
    
    # 最後のチャンクを追加
    if current_chunk:
        result.append(current_chunk)
    
    return result

def save_to_notion(notion_client, database_id: str, video: VideoInfo):
    try:
        # 重複チェック
        results = notion_client.databases.query(
            database_id=database_id,
            filter={
                "property": "URL",
                "rich_text": {
                    "contains": video.video_id
                }
            }
        )

        if results['results']:
            logging.info(f"スキップ: 動画 {video.video_id} は既にNotionに存在します")
            return

        blocks = []

        # 説明文を分割して各ブロックに追加
        description_parts = split_text(video.description)
        for part in description_parts:
            blocks.append({
                "object": "block",
                "type": "paragraph",
                "paragraph": {
                    "rich_text": [{"type": "text", "text": {"content": part}}]
                }
            })

        if video.caption_summary:
            blocks.append({
                "object": "block",
                "type": "heading_2",
                "heading_2": {
                    "rich_text": [{"type": "text", "text": {"content": "字幕要約"}}]
                }
            })

            # 各要約を個別のブロックとして追加
            for summary in video.caption_summary:
                blocks.append({
                    "object": "block",
                    "type": "paragraph",
                    "paragraph": {
                        "rich_text": [{
                            "type": "text",
                            "text": {
                                "content": f"{summary['chunk_number']}/{summary['total_chunks']} \n{summary['content']}"
                            }
                        }]
                    }
                })

        notion_client.pages.create(
            parent={"database_id": database_id},
            properties={
                "Title": {
                    "title": [{"text": {"content": video.title}}]
                },
                "URL": {
                    "url": video.url
                },
                "Channel": {
                    "multi_select": [{"name": video.channel_title}]
                }
            },
            children=blocks
        )
        
    except Exception as e:
        logging.error(f"Notion保存エラー: {e}")
        raise

def chunk_text(text: str, chunk_size: int = 3000) -> List[str]:
    """テキストを指定された文字数で分割
    
    Args:
        text (str): 分割するテキスト
        chunk_size (int, optional): 1チャンクの文字数. Defaults to 500.
    
    Returns:
        List[str]: 分割されたテキストのリスト
    """
    return [text[i:i+chunk_size] for i in range(0, len(text), chunk_size)]

def summarize_long_caption(groq_client, caption_text: str, language: str = "ja") -> List[Dict]:
    chunks = chunk_text(caption_text)
    summaries = []
    
    chunk_prompt_template = """
    【要約対象の字幕テキスト】
    {chunk}

    【要約の条件】
    1. タイトルをつける
    2. 重要ポイントを3~4個で箇条書きにする
    3. 「だ・である」調で書く
    4. 全体を300文字以内でまとめる

    以下の形式で要約を作成してください：

    【タイトル】
    （ここにタイトルを記入）

    【重要ポイント】
    • （ポイント1）
    • （ポイント2）
    • （ポイント3）
    ※必要に応じて4つ目のポイントを追加

    【要約】
    （ここに要約を記入）
    """
    for i, chunk in enumerate(chunks, 1):
        try:
            logging.info(f"チャンク {i}/{len(chunks)}を要約中...")
            
            chat_completion = groq_client.chat.completions.create(
                messages=[{
                    "role": "user",
                    "content": chunk_prompt_template.format(chunk=chunk)
                }],
                model="mixtral-8x7b-32768",
                temperature=0.4,
            )
            
            summary = chat_completion.choices[0].message.content
            summaries.append({
                "chunk_number": i,
                "total_chunks": len(chunks),
                "content": summary
            })
            logging.info(f"チャンク {i} の要約が完了")
            
            time.sleep(2)
            
        except Exception as e:
            error_msg = f"チャンク {i} の要約中にエラー: {e}"
            logging.error(error_msg)
            summaries.append({
                "chunk_number": i,
                "total_chunks": len(chunks),
                "content": error_msg
            })

    return summaries

# process_channel関数内の字幕要約部分を修正
def process_channel(youtube_service, notion_client, groq_client, channel_id: str, database_id: str):
    try:
        videos = get_latest_videos(youtube_service, channel_id)
        
        for video in videos:
            captions = get_captions(video.video_id)
            
            if captions:
                first_caption = captions[0]
                video.caption_summary = summarize_long_caption(
                    groq_client,
                    first_caption.text
                )

            save_to_notion(notion_client, database_id, video)
            
    except Exception as e:
        logging.error(f"チャンネル処理エラー: {e}")

def main():
    # .envファイルから環境変数を読み込む
    load_dotenv()

    # ログ設定
    logging.basicConfig(
        level=logging.INFO,
        format='%(asctime)s [%(levelname)s] %(message)s'
    )

    # 環境変数の取得
    notion_api_key = os.getenv("NOTION_API_KEY")
    notion_database_id = os.getenv("NOTION_DATABASE_ID")
    groq_api_key = os.getenv("GROQ_API_KEY")

    if not all([notion_api_key, notion_database_id, groq_api_key]):
        raise ValueError("Required environment variables are not set")

    # クライアントの初期化
    youtube_service = get_service_account_client()
    notion_client = Client(auth=notion_api_key)
    groq_client = get_groq_client()

    # チャンネルIDのリスト
    channel_ids = [
        "UCagAVZFPcLh9UMDidIUfXKQ",  # MBチャンネル
        "UC67Wr_9pA4I0glIxDt_Cpyw",  # 学長
        "UCXjTiSGclQLVVU83GVrRM4w",  # ホリエモン
    ]

    # チャンネルごとの処理
    for channel_id in channel_ids:
        process_channel(youtube_service, notion_client, groq_client, channel_id, notion_database_id)

if __name__ == "__main__":
    main()