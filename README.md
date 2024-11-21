## 概要

youtubeの概要欄を取得し、字幕をqrogを用いて要約してnotionに保存するツールです。

## setup

### python
```bash
python3 -m venv youtube
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

## lambda化

1. 必要なパッケージをインストール
```bash
pip install -r requirements.txt --target ./python
```

2. **ZIPファイルの作成**
```bash
# lambda_packageディレクトリ内で
zip -r ../lambda_deployment.zip .
```
