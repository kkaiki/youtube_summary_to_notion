package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "os"
    "time"
    "strings"
    "sync"
    "github.com/jomei/notionapi"
    "google.golang.org/api/youtube/v3"
    "google.golang.org/api/option"
)

const (
    MaxDescriptionLength = 2000
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
    youtubeAPIKey := os.Getenv("YOUTUBE_API_KEY")
    notionAPIKey := os.Getenv("NOTION_API_KEY")
    notionDatabaseID := os.Getenv("NOTION_DATABASE_ID")

    if youtubeAPIKey == "" || notionAPIKey == "" || notionDatabaseID == "" {
        log.Fatal("Required environment variables are not set")
    }

    // クライアントの初期化
    ctx := context.Background()
    youtubeService, err := youtube.NewService(ctx, option.WithAPIKey(youtubeAPIKey))
    if err != nil {
        log.Fatalf("Error creating YouTube client: %v", err)
    }

    notionClient := notionapi.NewClient(notionapi.Token(notionAPIKey))

    // チャンネルIDのリスト
    channelIDs := []string{
        "UCagAVZFPcLh9UMDidIUfXKQ", // MBチャンネル
		
    }

    // チャンネルごとの処理
    for _, channelID := range channelIDs {
        processChannel(ctx, youtubeService, notionClient, channelID, notionDatabaseID)
    }
}

func processChannel(ctx context.Context, youtubeService *youtube.Service, notionClient *notionapi.Client, channelID, databaseID string) {
    videos, err := getLatestVideos(youtubeService, channelID)
    if err != nil {
        log.Printf("Error getting videos for channel %s: %v", channelID, err)
        return
    }

    var wg sync.WaitGroup
    semaphore := make(chan struct{}, 3) // 同時実行数を制限

    for _, video := range videos {
        wg.Add(1)
        go func(v VideoInfo) {
            defer wg.Done()
            semaphore <- struct{}{} // セマフォの獲得
            defer func() { <-semaphore }() // セマフォの解放

            // 重複チェック
            exists, err := checkDuplicateInNotion(notionClient, databaseID, v.VideoID)
            if err != nil {
                log.Printf("Error checking duplicate: %v", err)
                return
            }
            if exists {
                log.Printf("Video %s already exists in Notion", v.VideoID)
                return
            }

            // 字幕の取得
            captions, err := getCaptions(youtubeService, v.VideoID)
            if err != nil {
                log.Printf("Error getting captions for video %s: %v", v.VideoID, err)
            }
            v.Captions = captions

            // Notionへの保存
            err = saveToNotionWithRetry(notionClient, databaseID, v, 3)
            if err != nil {
                log.Printf("Error saving to Notion: %v", err)
                return
            }
            log.Printf("Successfully saved video: %s", v.Title)
        }(video)
    }

    wg.Wait()
}

func getLatestVideos(service *youtube.Service, channelID string) ([]VideoInfo, error) {
    channelResponse, err := service.Channels.List([]string{"contentDetails"}).
        Id(channelID).
        Do()
    if err != nil {
        return nil, fmt.Errorf("error getting channel info: %v", err)
    }

    if len(channelResponse.Items) == 0 {
        return nil, fmt.Errorf("channel not found")
    }

    uploadsPlaylistID := channelResponse.Items[0].ContentDetails.RelatedPlaylists.Uploads

    var videos []VideoInfo
    playlistResponse, err := service.PlaylistItems.List([]string{"snippet"}).
        PlaylistId(uploadsPlaylistID).
        MaxResults(10).
        Do()
    if err != nil {
        return nil, fmt.Errorf("error getting playlist items: %v", err)
    }

    for _, item := range playlistResponse.Items {
        publishedAt, _ := time.Parse(time.RFC3339, item.Snippet.PublishedAt)
        video := VideoInfo{
            VideoID:      item.Snippet.ResourceId.VideoId,
            Title:        item.Snippet.Title,
            Description:  truncateDescription(item.Snippet.Description),
            PublishedAt:  publishedAt,
            ChannelTitle: item.Snippet.ChannelTitle,
            URL:         fmt.Sprintf("https://www.youtube.com/watch?v=%s", item.Snippet.ResourceId.VideoId),
        }
        videos = append(videos, video)
    }

    return videos, nil
}

func getCaptions(service *youtube.Service, videoID string) ([]CaptionInfo, error) {
    captionResponse, err := service.Captions.List([]string{"snippet"}, videoID).Do()
    if err != nil {
        return nil, fmt.Errorf("error getting captions: %v", err)
    }

    var captions []CaptionInfo
    for _, caption := range captionResponse.Items {
        captionInfo := CaptionInfo{
            Language:    caption.Snippet.Language,
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
    // 説明文を制限
    description := truncateDescription(video.Description)
    
    captionsJSON, err := json.Marshal(video.Captions)
    if err != nil {
        return fmt.Errorf("error marshaling captions: %v", err)
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
            "Description": notionapi.RichTextProperty{
                RichText: []notionapi.RichText{
                    {
                        Text: &notionapi.Text{
                            Content: description,
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
            "Captions": notionapi.RichTextProperty{
                RichText: []notionapi.RichText{
                    {
                        Text: &notionapi.Text{
                            Content: string(captionsJSON),
                        },
                    },
                },
            },
        },
    }

    _, err = client.Page.Create(context.Background(), params)
    return err
}