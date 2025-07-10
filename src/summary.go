package main

import (
	"io"
    "context"
    "fmt"
    "log"
    "os"
    "time"
    "strings"
    "sync"
    "encoding/json"
    "net/http"
    "github.com/jomei/notionapi"
    "google.golang.org/api/youtube/v3"
    "golang.org/x/oauth2"
    "golang.org/x/oauth2/google"
    "github.com/google/generative-ai-go/genai"
    "encoding/xml"
    "os/exec"
)

const (
    MaxDescriptionLength = 2000
    YouTubeScope = youtube.YoutubeReadonlyScope + " " + 
                  youtube.YoutubeForceSslScope
)

type VideoInfo struct {
    VideoID      string
    Title        string
    Description  string
    PublishedAt  time.Time
    ChannelTitle string
    URL          string
    Captions     []CaptionInfo
    Summary      string // 追加: Geminiによる要約
}

type CaptionInfo struct {
    Language    string
    Text        string
    IsAutomatic bool
}

// Gemini API用の構造体
type GeminiRequest struct {
    Contents []GeminiContent `json:"contents"`
}

type GeminiContent struct {
    Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
    Text string `json:"text"`
}

type GeminiResponse struct {
    Candidates []GeminiCandidate `json:"candidates"`
}

type GeminiCandidate struct {
    Content GeminiContent `json:"content"`
}

// RSSフィード用の構造体
// <feed><entry><yt:videoId>...</yt:videoId></yt:videoId></entry></feed>
type Feed struct {
    Entries []Entry `xml:"entry"`
}
type Entry struct {
    VideoID string `xml:"videoId"`
}

// 説明文を制限する関数
func truncateDescription(description string) string {
    runes := []rune(description)
    if len(runes) > MaxDescriptionLength {
        return string(runes[:MaxDescriptionLength-3]) + "..."
    }
    return description
}

// RSSフィードから最新動画IDを取得
func getLatestVideoIDFromRSS(channelID string) (string, error) {
    url := fmt.Sprintf("https://www.youtube.com/feeds/videos.xml?channel_id=%s", channelID)
    resp, err := http.Get(url)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", err
    }
    var feed Feed
    if err := xml.Unmarshal(body, &feed); err != nil {
        return "", err
    }
    if len(feed.Entries) == 0 {
        return "", fmt.Errorf("no videos found")
    }
    return feed.Entries[0].VideoID, nil
}

// yt-dlpで日本語字幕をダウンロード
func downloadJapaneseSubtitle(videoID string) error {
    url := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
    cmd := exec.Command("yt-dlp", "--write-auto-sub", "--sub-lang", "ja", "--skip-download", url)
    out, err := cmd.CombinedOutput()
    if err != nil {
        return fmt.Errorf("yt-dlp error: %v, output: %s", err, string(out))
    }
    return nil
}

func main() {
    // チャンネルIDのリスト
    channelIDs := []string{
        "UCagAVZFPcLh9UMDidIUfXKQ", // MBチャンネル
        "UC67Wr_9pA4I0glIxDt_Cpyw", // 学長
        "UCXjTiSGclQLVVU83GVrRM4w", // ホリエモン
    }
    for _, channelID := range channelIDs {
        videoID, err := getLatestVideoIDFromRSS(channelID)
        if err != nil {
            log.Printf("チャンネル%sの最新動画ID取得失敗: %v", channelID, err)
            continue
        }
        log.Printf("チャンネル%sの最新動画ID: %s", channelID, videoID)
        if err := downloadJapaneseSubtitle(videoID); err != nil {
            log.Printf("字幕ダウンロード失敗: %v", err)
            continue
        }
        log.Printf("字幕ダウンロード完了: %s", videoID)
    }
}

// getServiceAccountClient関数の修正
func getServiceAccountClient() (*http.Client, error) {
    data, err := os.ReadFile("service-account.json")
    if err != nil {
        return nil, fmt.Errorf("サービスアカウントキーファイルの読み込みエラー: %v", err)
    }

    // 利用可能なスコープのみを指定
    creds, err := google.CredentialsFromJSON(context.Background(), data, 
        youtube.YoutubeReadonlyScope,
        youtube.YoutubeForceSslScope)
    if err != nil {
        return nil, fmt.Errorf("認証情報の作成エラー: %v", err)
    }

    return oauth2.NewClient(context.Background(), creds.TokenSource), nil
}

func tokenFromFile(file string) (*oauth2.Token, error) {
    f, err := os.Open(file)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    tok := &oauth2.Token{}
    err = json.NewDecoder(f).Decode(tok)
    return tok, err
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
    authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
    fmt.Printf("認証URLを開きます: \n%v\n", authURL)
    
    // 入力待ちであることを明確に表示
    fmt.Print("認証コードを入力してください: ")
    
    var authCode string
    if _, err := fmt.Scan(&authCode); err != nil {
        log.Printf("認証コードの読み取りに失敗: %v", err)
        return nil
    }
    
    // 入力された認証コードを表示
    log.Printf("入力された認証コード: %s", authCode)
    
    // コンテキストにタイムアウトを設定
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    tok, err := config.Exchange(ctx, authCode)
    if err != nil {
        log.Printf("トークン取得に失敗: %v", err)
        return nil
    }
    
    log.Printf("トークン取得成功")
    return tok
}

func saveToken(path string, token *oauth2.Token) {
    fmt.Printf("Saving credential file to: %s\n", path)
    f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
    if err != nil {
        log.Fatalf("Unable to cache oauth token: %v", err)
    }
    defer f.Close()
    json.NewEncoder(f).Encode(token)
}

func getClient(config *oauth2.Config) *http.Client {
    tokFile := "token.json"
    tok, err := tokenFromFile(tokFile)
    if err != nil {
        // トークンファイルが存在しないか無効な場合のデバッグ出力
        log.Printf("トークンファイルの読み込みに失敗: %v", err)
        tok = getTokenFromWeb(config)
        saveToken(tokFile, tok)
    }
    return config.Client(context.Background(), tok)
}

// OAuthクライアントIDで認証する関数
func getOAuthClient() (*http.Client, error) {
    b, err := os.ReadFile("client_secret.json")
    if err != nil {
        return nil, fmt.Errorf("client_secret.jsonの読み込みエラー: %v", err)
    }
    config, err := google.ConfigFromJSON(b, youtube.YoutubeReadonlyScope, youtube.YoutubeForceSslScope)
    if err != nil {
        return nil, fmt.Errorf("OAuth2 config作成エラー: %v", err)
    }
    return getClient(config), nil
}
// processChannel 関数の修正
func processChannel(ctx context.Context, youtubeService *youtube.Service, notionClient *notionapi.Client, channelID, databaseID, geminiAPIKey string) {
    
    videos, err := getLatestVideos(youtubeService, channelID)
    if err != nil {
        log.Printf("エラー: チャンネル %s の動画取得に失敗: %v", channelID, err)
        return
    }
    log.Printf("チャンネル %s から %d 件の動画を取得しました", channelID, len(videos))

    var wg sync.WaitGroup
    semaphore := make(chan struct{}, 3)

    for _, video := range videos {
        wg.Add(1)
        go func(v VideoInfo) {
            defer wg.Done()
            semaphore <- struct{}{}
            defer func() { <-semaphore }()


            exists, err := checkDuplicateInNotion(notionClient, databaseID, v.VideoID)
            if err != nil {
                log.Printf("エラー: 重複チェック中 (VideoID: %s): %v", v.VideoID, err)
                return
            }
            if exists {
                log.Printf("スキップ: 動画 %s は既にNotionに存在します", v.VideoID)
                return
            }

            captions, err := getCaptions(youtubeService, v.VideoID)
            if err != nil {
                log.Printf("警告: 動画 %s の字幕取得に失敗: %v", v.VideoID, err)
            } else {
                log.Printf("字幕取得完了: %s (%d 件の字幕)", v.VideoID, len(captions))
            }
            v.Captions = captions

            // Gemini APIを使用して要約を生成
            summary, err := summarizeWithGemini(geminiAPIKey, v)
            if err != nil {
                log.Printf("警告: 動画 %s の要約生成に失敗: %v", v.VideoID, err)
                v.Summary = "要約の生成に失敗しました。"
            } else {
                v.Summary = summary
            }

            err = saveToNotionWithRetry(notionClient, databaseID, v, 3)
            if err != nil {
                log.Printf("エラー: Notionへの保存失敗 (VideoID: %s): %v", v.VideoID, err)
                return
            }
        }(video)
    }

    wg.Wait()
}

// getLatestVideos 関数の修正
func getLatestVideos(service *youtube.Service, channelID string) ([]VideoInfo, error) {
    channelResponse, err := service.Channels.List([]string{"contentDetails"}).
        Id(channelID).
        Do()
    if err != nil {
        log.Printf("チャンネル情報取得エラー: %v", err)
        return nil, err
    }
    log.Printf("チャンネル情報取得成功")

    if len(channelResponse.Items) == 0 {
        return nil, fmt.Errorf("チャンネルが見つかりません")
    }

    uploadsPlaylistID := channelResponse.Items[0].ContentDetails.RelatedPlaylists.Uploads
    log.Printf("アップロードプレイリストID: %s", uploadsPlaylistID)

    var videos []VideoInfo
    nextPageToken := ""
    now := time.Now().In(time.Local)
    today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
    filteredCount := 0
    for {
        playlistCall := service.PlaylistItems.List([]string{"snippet"}).
            PlaylistId(uploadsPlaylistID).
            MaxResults(50)
        if nextPageToken != "" {
            playlistCall = playlistCall.PageToken(nextPageToken)
        }
        playlistResponse, err := playlistCall.Do()
        if err != nil {
            return nil, fmt.Errorf("プレイリストアイテムの取得に失敗: %v", err)
        }
        for _, item := range playlistResponse.Items {
            publishedAt, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
            if err != nil {
                log.Printf("警告: 動画 %s の日付解析に失敗: %v", item.Snippet.ResourceId.VideoId, err)
                continue
            }
            publishedAtJST := publishedAt.In(time.Local)
            publishedDate := time.Date(publishedAtJST.Year(), publishedAtJST.Month(), publishedAtJST.Day(), 0, 0, 0, 0, time.Local)
            if publishedDate.Equal(today) {
                video := VideoInfo{
                    VideoID:      item.Snippet.ResourceId.VideoId,
                    Title:        item.Snippet.Title,
                    Description:  truncateDescription(item.Snippet.Description),
                    PublishedAt:  publishedAt,
                    ChannelTitle: item.Snippet.ChannelTitle,
                    URL:         fmt.Sprintf("https://www.youtube.com/watch?v=%s", item.Snippet.ResourceId.VideoId),
                }
                videos = append(videos, video)
                filteredCount++
                log.Printf("今日の動画を追加: %s (%s)", video.Title, video.PublishedAt.Format("2006-01-02 15:04:05"))
            }
        }
        if playlistResponse.NextPageToken == "" {
            break
        }
        nextPageToken = playlistResponse.NextPageToken
    }
    log.Printf("チャンネル %s から今日の動画 %d 件を抽出しました", channelID, filteredCount)
    return videos, nil
}
func getCaptions(service *youtube.Service, videoID string) ([]CaptionInfo, error) {
    captionResponse, err := service.Captions.List([]string{"snippet"}, videoID).Do()
    if err != nil {
        if strings.Contains(err.Error(), "forbidden") || 
           strings.Contains(err.Error(), "quotaExceeded") {
            log.Printf("警告: 動画 %s の字幕取得をスキップ: %v", videoID, err)
            return []CaptionInfo{}, nil
        }
        return nil, fmt.Errorf("字幕情報の取得エラー: %v", err)
    }

    var captions []CaptionInfo
    for _, caption := range captionResponse.Items {
        // 字幕テキストを取得
		resp, err := service.Captions.Download(caption.Id).Download()
		if err != nil {
			log.Printf("Error downloading caption: %v", err)
			continue
		}
		captionTrack, err := io.ReadAll(resp.Body)
		defer resp.Body.Close()
        captionInfo := CaptionInfo{
            Language:    caption.Snippet.Language,
            Text:        string(captionTrack),
            IsAutomatic: strings.Contains(caption.Snippet.TrackKind, "ASR"),
        }
        captions = append(captions, captionInfo)
    }

    return captions, nil
}

func checkDuplicateInNotion(client *notionapi.Client, databaseID, videoID string) (bool, error) {
    query := &notionapi.DatabaseQueryRequest{
        Filter: &notionapi.PropertyFilter{
            Property: "URL",
            RichText: &notionapi.TextFilterCondition{
                Contains: videoID,
            },
        },
    }
    
    result, err := client.Database.Query(context.Background(), notionapi.DatabaseID(databaseID), query)
    if err != nil {
        return false, err
    }
    
    return len(result.Results) > 0, nil
}

func saveToNotionWithRetry(client *notionapi.Client, databaseID string, video VideoInfo, maxRetries int) error {
    var lastErr error
    for i := 0; i < maxRetries; i++ {
        err := saveToNotion(client, databaseID, video)
        if err == nil {
            return nil
        }
        lastErr = err
        time.Sleep(time.Second * time.Duration(i+1))
    }
    return fmt.Errorf("failed after %d retries: %v", maxRetries, lastErr)
}


func saveToNotion(client *notionapi.Client, databaseID string, video VideoInfo) error {
    description := truncateDescription(video.Description)

	// ブロックの作成
	blocks := []notionapi.Block{
		&notionapi.Heading2Block{
			BasicBlock: notionapi.BasicBlock{
				Object: "block",
				Type:   notionapi.BlockTypeHeading2,
			},
			Heading2: notionapi.Heading{
				RichText: []notionapi.RichText{
					{
						Type: "text",
						Text: &notionapi.Text{
							Content: "要約",
						},
					},
				},
			},
		},
		&notionapi.ParagraphBlock{
			BasicBlock: notionapi.BasicBlock{
				Object: "block",
				Type:   notionapi.BlockTypeParagraph,
			},
			Paragraph: notionapi.Paragraph{
				RichText: []notionapi.RichText{
					{
						Type: "text",
						Text: &notionapi.Text{
							Content: video.Summary,
						},
					},
				},
			},
		},
		&notionapi.Heading2Block{
			BasicBlock: notionapi.BasicBlock{
				Object: "block",
				Type:   notionapi.BlockTypeHeading2,
			},
			Heading2: notionapi.Heading{
				RichText: []notionapi.RichText{
					{
						Type: "text",
						Text: &notionapi.Text{
							Content: "動画説明",
						},
					},
				},
			},
		},
		&notionapi.ParagraphBlock{
			BasicBlock: notionapi.BasicBlock{
				Object: "block",
				Type:   notionapi.BlockTypeParagraph,
			},
			Paragraph: notionapi.Paragraph{
				RichText: []notionapi.RichText{
					{
						Type: "text",
						Text: &notionapi.Text{
							Content: description,
						},
					},
				},
			},
		},
		&notionapi.Heading2Block{
			BasicBlock: notionapi.BasicBlock{
				Object: "block",
				Type:   notionapi.BlockTypeHeading2,
			},
			Heading2: notionapi.Heading{
				RichText: []notionapi.RichText{
					{
						Type: "text",
						Text: &notionapi.Text{
							Content: "字幕",
						},
					},
				},
			},
		},
	}

	// 字幕ブロックの追加
	for _, caption := range video.Captions {
		blocks = append(blocks, &notionapi.ParagraphBlock{
			BasicBlock: notionapi.BasicBlock{
				Object: "block",
				Type:   notionapi.BlockTypeParagraph,
			},
			Paragraph: notionapi.Paragraph{
				RichText: []notionapi.RichText{
					{
						Type: "text",
						Text: &notionapi.Text{
							Content: fmt.Sprintf("言語: %s\n%s", caption.Language, caption.Text),
						},
					},
				},
			},
		})
	}
	
	
    params := &notionapi.PageCreateRequest{
        Parent: notionapi.Parent{
            Type:       notionapi.ParentTypeDatabaseID,
            DatabaseID: notionapi.DatabaseID(databaseID),
        },
        Properties: notionapi.Properties{
            "Title": notionapi.TitleProperty{
                Title: []notionapi.RichText{
                    {
                        Text: &notionapi.Text{
                            Content: video.Title,
                        },
                    },
                },
            },
            "URL": notionapi.URLProperty{
                URL: video.URL,
            },
            "Channel": notionapi.MultiSelectProperty{
                MultiSelect: []notionapi.Option{
                    {
                        Name: video.ChannelTitle,
                    },
                },
            },
        },
        Children: blocks,
    }

    _, err := client.Page.Create(context.Background(), params)
    return err
}

// Gemini APIを使用して動画内容を要約する関数（genaiパッケージ版）
func summarizeWithGemini(apiKey string, video VideoInfo) (string, error) {
    ctx := context.Background()
    // APIキーは環境変数 GEMINI_API_KEY から自動で取得される
    client, err := genai.NewClient(ctx)
    if err != nil {
        return "", fmt.Errorf("Geminiクライアントの作成エラー: %v", err)
    }
    defer client.Close()

    // 要約用のプロンプトを作成
    prompt := fmt.Sprintf(`以下のYouTube動画の内容を日本語で要約してください。

動画タイトル: %s
動画説明: %s

字幕内容:
`, video.Title, video.Description)

    // 字幕を追加（最初の日本語字幕または自動生成字幕を使用）
    var captionText string
    for _, caption := range video.Captions {
        if caption.Language == "ja" || caption.Language == "ja-JP" {
            captionText = caption.Text
            break
        }
    }
    if captionText == "" {
        for _, caption := range video.Captions {
            if !caption.IsAutomatic {
                captionText = caption.Text
                break
            }
        }
    }
    if captionText == "" && len(video.Captions) > 0 {
        captionText = video.Captions[0].Text
    }
    if captionText != "" {
        if len(captionText) > 8000 {
            captionText = captionText[:8000] + "..."
        }
        prompt += captionText
    } else {
        prompt += "字幕は利用できません。"
    }
    prompt += `

以下の形式で要約してください：
1. 動画の主要なポイント（3-5個）
2. 重要な発言や引用
3. 結論やまとめ

要約は500文字以内で簡潔にまとめてください。`

    model := client.GenerativeModel("gemini-pro")
    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil {
        return "", fmt.Errorf("Gemini APIリクエストエラー: %v", err)
    }
    if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
        return "", fmt.Errorf("Gemini APIからの有効なレスポンスがありません")
    }
    // パートの内容を文字列として取得
    var summary string
    for _, part := range resp.Candidates[0].Content.Parts {
        summary += fmt.Sprint(part)
    }
    log.Printf("要約完了: %s (%d文字)", video.Title, len(summary))
    return summary, nil
}
