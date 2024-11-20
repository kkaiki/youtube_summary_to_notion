

## setup

```bash
go mod init github.com/kaiki/summary_youtube
go get google.golang.org/api/youtube/v3
go get github.com/jomei/notionapi


## Command list

### How to run

```bash
go build -o summary summary.go && ./summary
```

### How to comnpile

```bash
go build -o summary summary.go
GOOS=linux GOARCH=amd64 go build -o bootstrap summary.go
zip function.zip bootstrap
```

### 注意点

字幕がエラーとなる場合は、それはライブストリーム配信の予定。