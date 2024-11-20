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
    "github.com/jomei/notionapi"
	"google.golang.org/api/option"
    "google.golang.org/api/youtube/v3"
    "net/http"
	"encoding/json"
    "golang.org/x/oauth2"
    "golang.org/x/oauth2/google"
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
}

type CaptionInfo struct {
    Language    string
    Text        string
    IsAutomatic bool
}

// 説明文を制限する関数
func truncateDescription(description string) string {
    runes := []rune(description)
    if len(runes) > MaxDescriptionLength {
        return string(runes[:MaxDescriptionLength-3]) + "..."
    }
    return description
}

func main() {
    // 環境変数の取得
    notionAPIKey := os.Getenv("NOTION_API_KEY")
    notionDatabaseID := os.Getenv("NOTION_DATABASE_ID")
    if notionAPIKey == "" || notionDatabaseID == "" {
        log.Fatal("Required environment variables are not set")
    }

    ctx := context.Background()

    // サービスアカウント認証クライアントの取得
    client, err := getServiceAccountClient()
    if err != nil {
        log.Fatalf("サービスアカウントクライアントの作成に失敗: %v", err)
    }

    // YouTubeサービスの初期化
    youtubeService, err := youtube.NewService(ctx, option.WithHTTPClient(client))
    if err != nil {
        log.Fatalf("YouTubeサービスの作成に失敗: %v", err)
    }

    // Notionクライアントの初期化
    notionClient := notionapi.NewClient(notionapi.Token(notionAPIKey))

    // チャンネルIDのリスト
    channelIDs := []string{
        "UCagAVZFPcLh9UMDidIUfXKQ", // MBチャンネル
        "UC67Wr_9pA4I0glIxDt_Cpyw", // 学長
        "UCXjTiSGclQLVVU83GVrRM4w", // ホリエモン
    }

    // チャンネルごとの処理
    for _, channelID := range channelIDs {
        processChannel(ctx, youtubeService, notionClient, channelID, notionDatabaseID)
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
// processChannel 関数の修正
func processChannel(ctx context.Context, youtubeService *youtube.Service, notionClient *notionapi.Client, channelID, databaseID string) {
    
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
    playlistResponse, err := service.PlaylistItems.List([]string{"snippet"}).
        PlaylistId(uploadsPlaylistID).
        MaxResults(50).
        Do()
    if err != nil {
        return nil, fmt.Errorf("プレイリストアイテムの取得に失敗: %v", err)
    }

    now := time.Now().In(time.Local)
    today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
    yesterday := today.AddDate(0, 0, -1)

    filteredCount := 0
    for _, item := range playlistResponse.Items {
        publishedAt, err := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
        if err != nil {
            log.Printf("警告: 動画 %s の日付解析に失敗: %v", item.Snippet.ResourceId.VideoId, err)
            continue
        }
        
        publishedAtJST := publishedAt.In(time.Local)
        publishedDate := time.Date(publishedAtJST.Year(), publishedAtJST.Month(), publishedAtJST.Day(), 0, 0, 0, 0, time.Local)

        if publishedDate.Equal(today) || publishedDate.Equal(yesterday) {
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
            log.Printf("対象動画を追加: %s (%s)", video.Title, video.PublishedAt.Format("2006-01-02 15:04:05"))
        }
    }

    log.Printf("チャンネル %s から %d 件の対象動画を抽出しました", channelID, filteredCount)
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
