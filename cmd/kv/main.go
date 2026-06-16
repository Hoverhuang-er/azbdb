package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/docopt/docopt-go"
	"github.com/Hoverhuang-er/azbdb/pkg/kv"
)

const version = "0.1"

var (
	subcommandFuncs = map[string]func(*subcommandArgs) int{}
	subcommandUsage = map[string]string{}
	subcommandDesc  = map[string]string{}
)

type subcommandArgs struct {
	// inputs
	AccountName   string   `docopt:"--account"`
	AccountKey    string   `docopt:"--account-key"`
	ServiceURL    string   `docopt:"--endpoint"`
	Container     string   `docopt:"--container"`
	Prefix        string   `docopt:"-p,--prefix"`
	MasterKeyFile string   `docopt:"-k,--master-key-file"`
	Quiet         bool     `docopt:"-q,--quiet"`
	Verbose       bool     `docopt:"-v,--verbose"`
	Subcommand    string   `docopt:"<command>"`
	Arg           []string `docopt:"<arg>"`
	Ctx           context.Context
	Stdout        io.Writer
	Stderr        io.Writer

	// derived
	encryptor         kv.Encryptor
	SubcommandOptions docopt.Opts

	// outputs
	db       *kv.DB
	openOpts *kv.OpenOptions
	Result   struct {
		suppressCommit bool
	}
}

func main() {
	s := subcommandArgs{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Ctx:    context.Background(),
	}
	os.Exit(int(s.run(os.Args[1:])))
}

func parseArgs(s *subcommandArgs, args []string) {
	usage := `kv v` + version + `

Usage:
  kv --container=<name> [--account=<name>] [--account-key=<key>] [--endpoint=<url>]
	 [--master-key-file=<path>] [--prefix=<azure-prefix>] [-qv] <command> [<arg>...]
  kv -h

Options:
      --account=<name>       Azure Storage Account name; defaults to AZURE_STORAGE_ACCOUNT.
      --account-key=<key>    Azure Storage Account key; defaults to AZURE_STORAGE_KEY.
      --container=<name>     Azure Blob container for the database.
      --endpoint=<url>       Azure Blob service URL; defaults to AZURE_STORAGE_BLOB_ENDPOINT
                             or https://<account>.blob.core.windows.net/.
  -h, --help                 Print detailed help, including subcommands.
  -k, --master-key-file=<path>
                             path to master key material bytes
  -p, --prefix=<string>      Azure Blob name prefix
  -q, --quiet                suppress warnings
  -v, --verbose              always say what happened

Environment:
  AZURE_STORAGE_ACCOUNT      Azure Storage Account name.
  AZURE_STORAGE_KEY          Azure Storage Account key.
  AZURE_STORAGE_BLOB_ENDPOINT
                             Azure Blob service URL override.

Commands:
`
	cmds := []string{}
	for cmd := range subcommandUsage {
		cmds = append(cmds, cmd)
	}
	sort.Strings(cmds)
	for _, cmd := range cmds {
		usage += fmt.Sprintf("  %s\n", subcommandUsage[cmd])
		usage += fmt.Sprintf("    %s\n", subcommandDesc[cmd])
	}
	p := docopt.Parser{
		OptionsFirst: true,
	}
	opts, err := p.ParseArgs(usage, args, version)
	if err != nil {
		panic(err)
	}
	err = opts.Bind(s)
	if err != nil {
		panic(err)
	}
}

func (s *subcommandArgs) run(args []string) int {
	parseArgs(s, args)
	if s.MasterKeyFile != "" {
		keyBytes, err := ioutil.ReadFile(s.MasterKeyFile)
		if err != nil {
			fmt.Fprintln(s.Stderr, err)
			return 1
		}
		s.encryptor = kv.V1NodeEncryptor(keyBytes)
	}
	if f, ok := subcommandFuncs[s.Subcommand]; ok {
		su := subcommandUsage[s.Subcommand]
		var r int
		r = parseSubcommandArgs(su, s)
		if r != 0 {
			return r
		}
		r = f(s)
		if r != 0 {
			return r
		}
	} else {
		fmt.Fprintf(s.Stderr, "unknown command: %s", s.Subcommand)
		fmt.Fprintf(s.Stderr, "arg: %v\n", s.Arg)
		return 1
	}
	if s.db == nil ||
		s.openOpts == nil ||
		s.openOpts.ReadOnly ||
		s.Result.suppressCommit {
		return 0
	}
	if !s.db.IsDirty() {
		if s.Verbose {
			fmt.Fprintf(s.Stdout, "no change\n")
		}
		return 0
	}
	hash, err := s.db.Commit(s.Ctx)
	if err != nil {
		fmt.Fprintln(s.Stderr, err)
		return 1
	}
	if s.Verbose {
		if hash != nil {
			fmt.Fprintf(s.Stdout, "committed %s\n", *hash)
		} else {
			fmt.Fprintf(s.Stdout, "committed empty tree\n")
		}
	}
	return 0
}

func open(ctx context.Context, opts *kv.OpenOptions, args *subcommandArgs) *kv.DB {
	if args.Container == "" {
		fmt.Fprintf(args.Stderr, "--container not set\n")
		os.Exit(1)
	}
	client := getAzureBlobClient(args)

	cfg := kv.Config{
		Storage: &kv.AzureBlobStorageInfo{
			ServiceURL:    args.ServiceURL,
			ContainerName: args.Container,
			Prefix:        args.Prefix,
		},
		KeysLike:      "stringy",
		ValuesLike:    "stringy",
		NodeEncryptor: args.encryptor,
	}
	var so kv.OpenOptions
	if opts != nil {
		so = *opts
	}
	db, err := kv.Open(ctx, client, cfg, so, time.Now())
	if err != nil {
		err = fmt.Errorf("open: %w", err)
		fmt.Fprintln(args.Stderr, err)
		os.Exit(1)
	}

	args.db = db
	args.openOpts = &so
	return db
}

func getAzureBlobClient(args *subcommandArgs) kv.AzureBlobClient {
	accountName := args.AccountName
	if accountName == "" {
		accountName = os.Getenv("AZURE_STORAGE_ACCOUNT")
	}
	accountKey := args.AccountKey
	if accountKey == "" {
		accountKey = os.Getenv("AZURE_STORAGE_KEY")
	}
	serviceURL := args.ServiceURL
	if serviceURL == "" {
		serviceURL = os.Getenv("AZURE_STORAGE_BLOB_ENDPOINT")
	}
	client, err := kv.NewAzureBlobClient(kv.AzureBlobClientOptions{
		AccountName: accountName,
		AccountKey:  accountKey,
		ServiceURL:  serviceURL,
	})
	if err != nil {
		err = fmt.Errorf("azure blob client: %w", err)
		fmt.Fprintln(args.Stderr, err)
		os.Exit(1)
	}
	return client
}

func parseSubcommandArgs(usage string, s *subcommandArgs) int {
	p := docopt.Parser{
		SkipHelpFlags: true,
	}
	opts, err := p.ParseArgs(
		"Usage: "+strings.Split(usage, "\n")[0],
		s.Arg, "")
	if err != nil {
		fmt.Fprintln(s.Stderr, err)
		return 1
	}
	s.SubcommandOptions = opts
	return 0
}

func parseDuration(o *docopt.Opts, name string, d *time.Duration) error {
	durstr, err := o.String(name)
	if err != nil {
		return fmt.Errorf("option: %w", err)
	}
	if durstr == "" {
		return errors.New("empty duration")
	}
	*d, err = time.ParseDuration(durstr)
	if err != nil {
		return fmt.Errorf("duration: %w", err)
	}
	return nil
}
