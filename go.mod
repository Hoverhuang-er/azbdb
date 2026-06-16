module github.com/Hoverhuang-er/azbdb

go 1.25.0

require (
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.6.3
	github.com/docopt/docopt-go v0.0.0-20180111231733-ee0de3bc6815
	github.com/johncgriffin/overflow v0.0.0-20211019200055-46fa312c352c
	github.com/hoverhuang/mast v1.2.33
	github.com/mattn/go-sqlite3 v1.14.45
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1
	github.com/segmentio/ksuid v1.0.4
	github.com/stretchr/testify v1.11.1
	go.riyazali.net/sqlite v0.0.0-20250204091031-8aa392720bb1
	golang.org/x/crypto v0.53.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.19.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/davecgh/go-spew => ./internal/third_party/go-spew

replace github.com/docopt/docopt-go => ./internal/third_party/docopt-go

replace github.com/hoverhuang/mast => ./internal/third_party/mast
