## 使用方法


## setup

### python
```bash
pip install -r requirements.txt
. youtube/bin/activate
```

### go
```bash
go mod init github.com/kaiki/summary_youtube
go get google.golang.org/api/youtube/v3
go get github.com/jomei/notionapi
```

### Command list

#### How to run
```bash
go build -o summary summary.go && ./summary
```

### How to comnpile

```bash
go build -o summary summary.go
GOOS=linux GOARCH=amd64 go build -o bootstrap summary.go
zip function.zip bootstrap
```

