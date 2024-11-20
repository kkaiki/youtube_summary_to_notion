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


いいえ、そのままアップロードするだけでは動作しません。AWS Lambdaで正しく動作させるためには、以下の手順で準備する必要があります：

## 正しい準備手順

1. **依存関係のインストール**
```bash
# プロジェクトディレクトリを作成
mkdir lambda_package
cd lambda_package

# 必要なパッケージをインストール
pip install --target ./python google-auth google-api-python-client notion-client youtube-transcript-api groq python-dotenv pytz
```

2. **ファイルの配置**
```
lambda_package/
├── python/           # pip installで作成された依存関係
│   └── ...
├── lambda_function.py
├── service-account.json
└── .env             # 本番環境では不要（環境変数として設定）
```

3. **ZIPファイルの作成**
```bash
# lambda_packageディレクトリ内で
zip -r ../lambda_deployment.zip .
```

## 注意点


1. **パッケージサイズ**
- ZIPファイルが50MB以上になる場合は:
  - Lambda Layerの使用を検討
  - 不要なファイルの削除
  - 依存関係の最適化

1. **メモリと実行時間**
- Lambda関数の設定で:
  - メモリ: 512MB以上
  - タイムアウト: 15分（900秒）
を設定することを推奨

これらの手順に従って準備することで、Lambda環境で正しく動作するパッケージを作成できます。

Citations:
[1] https://ppl-ai-file-upload.s3.amazonaws.com/web/direct-files/27657036/74ecccc2-a2de-404a-9d68-d2a45829d757/paste.txt