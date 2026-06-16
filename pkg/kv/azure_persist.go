package kv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
)

var ErrBlobNotFound = errors.New("azure blob not found")

type AzureBlobClient interface {
	StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error
	LoadBlob(ctx context.Context, containerName, blobName string) ([]byte, error)
	DeleteBlob(ctx context.Context, containerName, blobName string) error
	ListBlobs(ctx context.Context, containerName, prefix string) ([]string, error)
	URL() string
}

type AzureBlobClientOptions struct {
	ConnectionString string
	AccountName      string
	AccountKey       string
	ServiceURL       string
}

type azureBlobClient struct {
	client *azblob.Client
}

func NewAzureBlobClient(opts AzureBlobClientOptions) (AzureBlobClient, error) {
	if opts.ConnectionString != "" {
		client, err := azblob.NewClientFromConnectionString(opts.ConnectionString, nil)
		if err != nil {
			return nil, fmt.Errorf("azure blob connection string: %w", err)
		}
		return azureBlobClient{client: client}, nil
	}
	if opts.AccountName == "" {
		return nil, fmt.Errorf("azure storage account name unset")
	}
	if opts.AccountKey == "" {
		return nil, fmt.Errorf("azure storage account key unset")
	}
	serviceURL := opts.ServiceURL
	if serviceURL == "" {
		serviceURL = "https://" + opts.AccountName + ".blob.core.windows.net/"
	}
	cred, err := azblob.NewSharedKeyCredential(opts.AccountName, opts.AccountKey)
	if err != nil {
		return nil, fmt.Errorf("azure shared key credential: %w", err)
	}
	client, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure blob client: %w", err)
	}
	return azureBlobClient{client: client}, nil
}

func (c azureBlobClient) StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error {
	_, err := c.client.UploadBuffer(ctx, containerName, blobName, value, nil)
	return err
}

func (c azureBlobClient) LoadBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	out, err := c.client.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		if isBlobNotFound(err) {
			return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, blobName)
		}
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func (c azureBlobClient) DeleteBlob(ctx context.Context, containerName, blobName string) error {
	_, err := c.client.DeleteBlob(ctx, containerName, blobName, nil)
	if isBlobNotFound(err) {
		return nil
	}
	return err
}

func (c azureBlobClient) ListBlobs(ctx context.Context, containerName, prefix string) ([]string, error) {
	pager := c.client.NewListBlobsFlatPager(containerName, &azblob.ListBlobsFlatOptions{Prefix: &prefix})
	res := []string{}
	for pager.More() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Segment.BlobItems {
			if obj.Name != nil {
				res = append(res, *obj.Name)
			}
		}
	}
	return res, nil
}

func (c azureBlobClient) URL() string { return c.client.URL() }

func isBlobNotFound(err error) bool {
	return errors.Is(err, ErrBlobNotFound) || bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ResourceNotFound, bloberror.ContainerNotFound)
}

type azurePersist struct {
	Client        AzureBlobClient
	ServiceURL    string
	ContainerName string
	Prefix        string
}

func (p azurePersist) Store(ctx context.Context, path string, value []byte) error {
	return p.Client.StoreBlob(ctx, p.ContainerName, p.Prefix+path, value)
}

func (p azurePersist) Load(ctx context.Context, path string) ([]byte, error) {
	return p.Client.LoadBlob(ctx, p.ContainerName, p.Prefix+path)
}

func (p azurePersist) NodeURLPrefix() string {
	serviceURL := p.ServiceURL
	if serviceURL == "" {
		serviceURL = p.Client.URL()
	}
	return strings.TrimRight(serviceURL, "/") + "/" + p.ContainerName + "/" + p.Prefix
}
