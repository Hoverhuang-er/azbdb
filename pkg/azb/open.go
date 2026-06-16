package azb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Hoverhuang-er/azbdb/internal/azbmetrics"
	"github.com/Hoverhuang-er/azbdb/pkg/kv"
	v1proto "github.com/Hoverhuang-er/azbdb/pkg/proto/v1"
	"github.com/hoverhuang/mast"
)

type KV struct {
	Root   *kv.DB
	Closer func()
}

var azureBlobClientFactory = kv.NewAzureBlobClient

func SetAzureBlobClientFactoryForTesting(factory func(kv.AzureBlobClientOptions) (kv.AzureBlobClient, error)) func() {
	old := azureBlobClientFactory
	azureBlobClientFactory = factory
	return func() { azureBlobClientFactory = old }
}

func OpenKV(ctx context.Context, azureOpts AzureOptions, subdir string) (*KV, error) {
	client, err := getAzureBlobClient(azureOpts)
	if err != nil {
		return nil, fmt.Errorf("azure blob client: %w", err)
	}
	path := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(azureOpts.Prefix, "/"), "/")+"/"+strings.TrimPrefix(subdir, "/"), "/")

	cfg := kv.Config{
		Storage: &kv.AzureBlobStorageInfo{
			ServiceURL:    azureOpts.ServiceURL,
			ContainerName: azureOpts.Container,
			Prefix:        path,
		},
		KeysLike:                     &Key{},
		ValuesLike:                   &v1proto.Row{},
		CustomMerge:                  mergeValues,
		CustomMarshal:                marshalProto,
		CustomUnmarshal:              unmarshalProto,
		MastNodeFormat:               string(mast.V1Marshaler),
		UnmarshalUsesRegisteredTypes: true,
	}
	if azureOpts.NodeCacheEntries > 0 {
		cfg.NodeCache = mast.NewNodeCache(azureOpts.NodeCacheEntries)
	}
	if azureOpts.EntriesPerNode > 0 {
		cfg.BranchFactor = uint(azureOpts.EntriesPerNode)
	}
	openOpts := kv.OpenOptions{
		ReadOnly:     azureOpts.ReadOnly,
		OnlyVersions: azureOpts.OnlyVersions,
	}
	s, err := kv.Open(ctx, client, cfg, openOpts, time.Now())
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	dbg("%s size:%d\n", subdir, s.Size())
	return &KV{Root: s}, nil
}

type AzureOptions struct {
	ConnectionString string
	AccountName      string
	AccountKey       string
	ServiceURL       string
	Container        string
	Prefix           string

	EntriesPerNode   int
	NodeCacheEntries int
	ReadOnly         bool
	OnlyVersions     []string
}

func getAzureBlobClient(opts AzureOptions) (kv.AzureBlobClient, error) {
	if err := azbmetrics.StartServer(); err != nil {
		return nil, err
	}
	if opts.ConnectionString == "" {
		opts.ConnectionString = os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
	}
	if opts.ConnectionString != "" {
		client, err := azureBlobClientFactory(kv.AzureBlobClientOptions{
			ConnectionString: opts.ConnectionString,
		})
		if err != nil {
			return nil, err
		}
		return azbmetrics.InstrumentAzureBlobClient(client), nil
	}
	if opts.AccountName == "" {
		opts.AccountName = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	if opts.AccountKey == "" {
		opts.AccountKey = os.Getenv("AZURE_STORAGE_KEY")
	}
	if opts.ServiceURL == "" {
		opts.ServiceURL = os.Getenv("AZURE_STORAGE_BLOB_ENDPOINT")
	}
	client, err := azureBlobClientFactory(kv.AzureBlobClientOptions{
		AccountName: opts.AccountName,
		AccountKey:  opts.AccountKey,
		ServiceURL:  opts.ServiceURL,
	})
	if err != nil {
		return nil, err
	}
	return azbmetrics.InstrumentAzureBlobClient(client), nil
}

func EnsureAzurePrefix(ctx context.Context, azureOpts AzureOptions) error {
	if azureOpts.ReadOnly {
		return nil
	}
	client, err := getAzureBlobClient(azureOpts)
	if err != nil {
		return fmt.Errorf("azure blob client: %w", err)
	}
	prefix := strings.Trim(strings.TrimSpace(azureOpts.Prefix), "/")
	if prefix == "" {
		return nil
	}
	return client.StoreBlob(ctx, azureOpts.Container, prefix+"/.azb", nil)
}
