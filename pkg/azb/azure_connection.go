package azb

import (
	"fmt"
	"strconv"
	"strings"
)

const DefaultAzureBlobPrefix = "sqlite_wal"

// AzureOptionsFromConnectionString extracts azb-specific settings from an Azure
// Storage connection string. Azure SDK ignores unknown keys, so callers may add
// ContainerName and Prefix to keep NewDB's public API to one string.
func AzureOptionsFromConnectionString(connectionString string) (AzureOptions, error) {
	parts, err := parseConnectionStringFields(connectionString)
	if err != nil {
		return AzureOptions{}, err
	}

	opts := AzureOptions{ConnectionString: connectionString}
	if v := firstConnectionStringValue(parts, "containername", "container", "blobcontainer", "blobcontainername", "azurecontainer"); v != "" {
		opts.Container = v
	}
	if v := firstConnectionStringValue(parts, "prefix", "azureprefix", "azbprefix", "database", "databasename"); v != "" {
		opts.Prefix = v
	}
	if v := firstConnectionStringValue(parts, "readonly", "read_only"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return AzureOptions{}, fmt.Errorf("readonly: %w", err)
		}
		opts.ReadOnly = b
	}
	return opts, nil
}

func parseConnectionStringFields(connectionString string) (map[string]string, error) {
	connectionString = strings.TrimSpace(strings.TrimRight(connectionString, ";"))
	if connectionString == "" {
		return nil, fmt.Errorf("azure connection string is empty")
	}

	fields := make(map[string]string)
	for _, part := range strings.Split(connectionString, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("malformed azure connection string field %q", part)
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return fields, nil
}

func firstConnectionStringValue(fields map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := fields[key]; value != "" {
			return value
		}
	}
	return ""
}

func joinAzurePrefix(base, elem string) string {
	base = strings.Trim(base, "/")
	elem = strings.Trim(elem, "/")
	switch {
	case base == "":
		return elem
	case elem == "":
		return base
	default:
		return base + "/" + elem
	}
}
