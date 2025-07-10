import os
import requests
import xml.etree.ElementTree as ET
from notion_client import Client as NotionClient
import google.generativeai as genai
import time
from youtube_transcript_api import YouTubeTranscriptApi, TranscriptsDisabled, NoTranscriptFound

# 追加: ローカル実行用
try:
    from dotenv import load_dotenv
    load_dotenv()
except ImportError:
    pass

def get_video_ids_from_channel(channel_id, api_key, max_results=3):
    url = (
        "https://www.googleapis.com/youtube/v3/search"
        f"?key={api_key}&channelId={channel_id}&part=snippet,id&order=date&maxResults={max_results}"
    )
    try:
        resp = requests.get(url)
        resp.raise_for_status()
        data = resp.json()
        video_ids = [
            item["id"]["videoId"]
            for item in data.get("items", [])
            if item["id"]["kind"] == "youtube#video"
        ]
        return video_ids
    except Exception as e:
        print(f"[ERROR] Exception in get_video_ids_from_channel: {e}")
        return []

def get_video_info(video_id, api_key):
    url = (
        "https://www.googleapis.com/youtube/v3/videos"
        f"?key={api_key}&id={video_id}&part=snippet"
    )
    try:
        resp = requests.get(url)
        resp.raise_for_status()
        data = resp.json()
        items = data.get("items", [])
        if not items:
            print(f"[DEBUG] No video info found for video_id={video_id}")
            return None, None, None
        snippet = items[0]["snippet"]
        title = snippet.get("title", "")
        description = snippet.get("description", "")
        channel = snippet.get("channelTitle", "")
        print(f"[DEBUG] Video info: title={title}, channel={channel}")
        return title, description, channel
    except Exception as e:
        print(f"[ERROR] Exception in get_video_info: {e}")
        return None, None, None

def get_japanese_caption(video_id, max_retries=5, wait_seconds=60):
    try:
        from youtube_transcript_api._errors import RequestBlocked, IPBlocked
    except ImportError:
        from youtube_transcript_api._errors import RequestBlocked
        IPBlocked = RequestBlocked  # ダミーで同じものを使う
    try:
        transcript = YouTubeTranscriptApi.get_transcript(video_id, languages=['ja'])
        texts = [item['text'] for item in transcript]
        print(f"[DEBUG] Number of caption lines: {len(texts)}")
        return "\n".join(texts)
    except (TranscriptsDisabled, NoTranscriptFound) as e:
        print(f"[DEBUG] No Japanese caption found for video_id={video_id}: {e}")
        return None
    except (RequestBlocked, IPBlocked) as e:
        print(f"[ERROR] IP block detected for video_id={video_id}: {e}. Aborting without retry.")
        return None
    except Exception as e:
        print(f"[ERROR] Exception in get_japanese_caption: {e}")
        return None

def summarize_with_gemini(api_key, caption, title, description):
    print(f"[DEBUG] summarize_with_gemini: title={title}, description={description[:30]}... (truncated)")
    try:
        genai.configure(api_key=api_key)
        prompt = f"""以下のYouTube動画の内容を日本語で要約してください。

動画タイトル: {title}
動画説明: {description}

字幕内容:
{caption}

"""
        model = genai.GenerativeModel("gemini-pro")
        response = model.generate_content(prompt)
        print(f"[DEBUG] Gemini response received")
        return response.text.strip() if hasattr(response, "text") else str(response)
    except Exception as e:
        print(f"[ERROR] Exception in summarize_with_gemini: {e}")
        return "要約生成中にエラーが発生しました。"

def save_to_notion(notion_token, database_id, video_info, summary):
    print(f"[DEBUG] save_to_notion: title={video_info['title']}")
    try:
        notion = NotionClient(auth=notion_token)
        notion.pages.create(
            parent={"database_id": database_id},
            properties={
                "Title": {"title": [{"text": {"content": video_info['title']}}]},
                "URL": {"url": video_info['url']},
                "Channel": {"multi_select": [{"name": video_info['channel']}]},
            },
            children=[
                {"object": "block", "type": "heading_2", "heading_2": {"rich_text": [{"type": "text", "text": {"content": "要約"}}]}},
                {"object": "block", "type": "paragraph", "paragraph": {"rich_text": [{"type": "text", "text": {"content": summary}}]}},
                {"object": "block", "type": "heading_2", "heading_2": {"rich_text": [{"type": "text", "text": {"content": "字幕"}}]}},
                {"object": "block", "type": "paragraph", "paragraph": {"rich_text": [{"type": "text", "text": {"content": video_info['caption']}}]}},
            ]
        )
        print(f"[DEBUG] Notion page created for: {video_info['title']}")
    except Exception as e:
        print(f"[ERROR] Exception in save_to_notion: {e}")

def lambda_handler(event, context):
    try:
        notion_token = os.environ.get("NOTION_API_KEY")
        database_id = os.environ.get("NOTION_DATABASE_ID")
        gemini_api_key = os.environ.get("GEMINI_API_KEY")
        youtube_api_key = os.environ.get("YOUTUBE_API_KEY")

        # APIキーの存在チェック
        if not notion_token:
            print("[ERROR] NOTION_API_KEY is not set.")
            return {"status": "error", "error": "NOTION_API_KEY is not set."}
        if not database_id:
            print("[ERROR] NOTION_DATABASE_ID is not set.")
            return {"status": "error", "error": "NOTION_DATABASE_ID is not set."}
        if not gemini_api_key:
            print("[ERROR] GEMINI_API_KEY is not set.")
            return {"status": "error", "error": "GEMINI_API_KEY is not set."}
        if not youtube_api_key:
            print("[ERROR] YOUTUBE_API_KEY is not set.")
            return {"status": "error", "error": "YOUTUBE_API_KEY is not set."}

        channel_ids = [
            "UCagAVZFPcLh9UMDidIUfXKQ", # MBチャンネル
            "UC67Wr_9pA4I0glIxDt_Cpyw", # 学長
            "UCXjTiSGclQLVVU83GVrRM4w", # ホリエモン
        ]
        for channel_id in channel_ids:
            video_ids = get_video_ids_from_channel(channel_id, youtube_api_key)
            for video_id in video_ids:
                print(f"[DEBUG] Processing video_id={video_id}")
                title, description, channel = get_video_info(video_id, youtube_api_key)
                if not title:
                    print(f"[DEBUG] Skipping video_id={video_id} due to missing video info")
                    continue
                url = f"https://www.youtube.com/watch?v={video_id}"

                caption = get_japanese_caption(video_id)
                if not caption:
                    print(f"[DEBUG] Skipping video_id={video_id} due to missing caption")
                    continue

                summary = summarize_with_gemini(gemini_api_key, caption, title, description)
                video_info = {
                    "title": title,
                    "url": url,
                    "channel": channel,
                    "caption": caption,
                }
                save_to_notion(notion_token, database_id, video_info, summary)

        return {"status": "done"}
    except Exception as e:
        print(f"[ERROR] Exception in lambda_handler: {e}")
        return {"status": "error", "error": str(e)}

# ローカル実行用
if __name__ == "__main__":
    lambda_handler({}, {})
