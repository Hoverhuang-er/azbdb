module github.com/Hoverhuang-er/azbdb/examples/agentmesh-azbdb-agent

go 1.25.0

require (
	github.com/Hoverhuang-er/azbdb v0.0.0
	github.com/mark3labs/mcp-go v0.20.1
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.19.1 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.2 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/storage/azblob v1.6.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/hoverhuang/mast v1.2.33 // indirect
	github.com/johncgriffin/overflow v0.0.0-20211019200055-46fa312c352c // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/mattn/go-sqlite3 v1.14.45 // indirect
	github.com/minio/blake2b-simd v0.0.0-20160723061019-3f5f724cb5b1 // indirect
	github.com/segmentio/ksuid v1.0.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.riyazali.net/sqlite v0.0.0-20250204091031-8aa392720bb1 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Hoverhuang-er/azbdb => ../..

replace github.com/hoverhuang/mast => ../../internal/third_party/mast

replace github.com/davecgh/go-spew => ../../internal/third_party/go-spew

replace github.com/docopt/docopt-go => ../../internal/third_party/docopt-go
