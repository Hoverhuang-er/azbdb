// Package kv turns your Azure Blob container into a diffable serverless key-value store.
//
// # Requirements
//
// - go1.18+
//
// - Azure Storage Account Blob object storage
//
// # Links
//
// * Merkle Search Tree, https://github.com/jrhy/mast
package kv

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	crdtpub "github.com/Hoverhuang-er/azbdb/pkg/kv/crdt"
	"github.com/Hoverhuang-er/azbdb/pkg/kv/internal/crdt"
	"github.com/jrhy/mast"
	"github.com/minio/blake2b-simd"
)

const (
	// DefaultBranchFactor is the number of entries that will be stored per Azure Blob object, if
	// not overridden by Config.BranchFactor.
	DefaultBranchFactor = 4096
)

var (
	// ErrReadOnly is the result of an attempt to Set or Commit on a tree that was not opened for writing.
	ErrReadOnly = errors.New("opened as read-only")
)

// DB is an Open()ed database.
type DB struct {
	readonly         bool
	blobClient       AzureBlobClient
	persist          mast.Persist
	root             *azurePersist
	merged           *azurePersist
	cfg              *Config
	crdt             crdt.Tree
	mergedRoots      map[string][]byte
	unmergeableRoots int
	tombstoned       bool
	kvVersion        int // crdt.Root.KVVersion, root format version (0, 1)
}

// Config defines how values are stored and (un)marshaled.
type Config struct {
	// Azure Blob container and database prefix
	Storage *AzureBlobStorageInfo
	// An optional label that will be added to committed versions' names for easier listing (e.g. "daily-report-")
	CustomRootPrefix string
	// An example of a key, to know what concrete type to make when unmarshaling
	KeysLike interface{}
	// An example of a value, to know what concrete type to make when unmarshaling
	ValuesLike interface{}
	// Sets the entries-per-Azure-Blob-object for new trees (ignored unless tree is empty)
	BranchFactor uint
	// An optional Azure Storage Account-scoped cache, instead of re-downloading recently-used tree nodes
	NodeCache mast.NodeCache
	// A way to keep your cloud provider out of your nodes
	NodeEncryptor Encryptor
	// OnConflictMerged is a callback that will be invoked whenever entries for a key have
	// different values in different trees that are being merged. It can be used to detect
	// when a uniqueness constraint has been broken and which keys need fixing.
	OnConflictMerged
	// LogFunc is a callback that will be invoked to provide details on potential corruption.
	LogFunc func(string)

	CustomMerge func(key interface{}, v1, v2 crdtpub.Value) crdtpub.Value

	MastNodeFormat string

	CustomMarshal func(interface{}) ([]byte, error)
	// CustomUnmarshal, if using registered types, will be invoked
	// to read tree nodes which are mast.PersistedNode with keys of
	// KeysLike, and values of crdtpub.Value of ValuesLike. All of
	// those should be registered before invoking kv.Open().
	CustomUnmarshal              func([]byte, interface{}) error
	UnmarshalUsesRegisteredTypes bool
}

// OnConflictMerged is a callback that will be invoked whenever entries for a key have
// different values in different trees that are being merged. It can be used to detect
// when a uniqueness constraint has been broken and which keys need fixing.
type OnConflictMerged func(key, v1, v2 interface{}) error

// Encryptor encrypts bytes with a nonce derived from the given path.
type Encryptor interface {
	Encrypt(path string, value []byte) ([]byte, error)
	Decrypt(path string, value []byte) ([]byte, error)
}

// AzureBlobStorageInfo describes where DB objects, including nodes and roots, are stored.
type AzureBlobStorageInfo struct {
	// ServiceURL is used to distinguish storage accounts for caching.
	ServiceURL string
	// ContainerName is where the node objects are stored.
	ContainerName string
	// Prefix is a blob name prefix that distinguishes objects for this tree.
	Prefix string
}

// OpenOptions control how databases are opened.
type OpenOptions struct {
	// ReadOnly means the database should not permit modifications.
	ReadOnly bool
	// OnlyVersions is used to see a tree as it looked for a set of historic versions.
	OnlyVersions []string
	// ForceRebranch is used to change the branch factor of an existing tree, rewriting nodes and roots if necessary.
	ForceRebranch bool
}

// Open returns a database. 'when' marks the creation time of the new version.
func Open(ctx context.Context, client AzureBlobClient, cfg Config, opts OpenOptions, when time.Time) (*DB, error) {
	if !opts.ReadOnly && len(opts.OnlyVersions) > 0 {
		return nil, fmt.Errorf("opts.OnlyVersions requires opts.ReadOnly")
	}
	if client == nil {
		return nil, fmt.Errorf("azure blob client must be non-nil")
	}
	if cfg.KeysLike == nil {
		return nil, fmt.Errorf("config KeysLike must be non-nil")
	}
	if cfg.Storage == nil {
		return nil, fmt.Errorf("config Storage must be non-nil")
	}
	if cfg.Storage.ContainerName == "" {
		return nil, fmt.Errorf("config Storage.ContainerName unset")
	}
	if cfg.Storage.ServiceURL == "" {
		cfg.Storage.ServiceURL = client.URL()
	}
	nodePersist := cfg.Storage.fixPrefix().toPersistEncrypt(client, "node/", cfg.NodeEncryptor)
	rootPersist := cfg.Storage.fixPrefix().toPersist(client, "root/current/")
	mergedPersist := cfg.Storage.fixPrefix().toPersist(client, "root/merged/")
	if cfg.BranchFactor == 0 {
		cfg.BranchFactor = DefaultBranchFactor
	}
	var (
		tree             *crdt.Tree
		mergedRoots      map[string][]byte
		unmergeableRoots int
	)
	marshal := marshalGob
	unmarshal := unmarshalGob
	unmarshalUsesRegisteredTypes := true
	if cfg.CustomMarshal != nil {
		marshal = cfg.CustomMarshal
		unmarshal = cfg.CustomUnmarshal
		unmarshalUsesRegisteredTypes = cfg.UnmarshalUsesRegisteredTypes
	} else {
		gob.Register(crdtpub.Value{})
		gob.Register(cfg.KeysLike)
		if cfg.ValuesLike != nil {
			gob.Register(cfg.ValuesLike)
		} else {
			gob.Register(map[string]interface{}{})
		}
	}
	crdtConfig := crdt.Config{
		KeysLike:                       cfg.KeysLike,
		ValuesLike:                     cfg.ValuesLike,
		StoreImmutablePartsWith:        nodePersist,
		NodeCache:                      cfg.NodeCache,
		MastNodeFormat:                 cfg.MastNodeFormat,
		Marshal:                        marshal,
		Unmarshal:                      unmarshal,
		UnmarshalerUsesRegisteredTypes: unmarshalUsesRegisteredTypes,
	}
	if cfg.CustomMerge != nil {
		crdtConfig.CustomMerge = cfg.CustomMerge
	}
	if cfg.OnConflictMerged != nil {
		crdtConfig.OnConflictMerged = crdt.OnConflictMerged(cfg.OnConflictMerged)
	}
	var versionsToLoad []string
	var skipUnreadable bool
	var kvVersion int
	var err error
	persists := []mast.Persist{rootPersist}
	if opts.OnlyVersions != nil {
		versionsToLoad = opts.OnlyVersions
		persists = []mast.Persist{mergedPersist, rootPersist}
		skipUnreadable = false
	} else {
		versionsToLoad, err = listRoots(ctx, client, rootPersist)
		if err != nil {
			return nil, err
		}
		skipUnreadable = true
	}
	tree, mergedRoots, unmergeableRoots, err = mergeRoots(ctx, versionsToLoad, cfg, crdtConfig, persists, when, opts.ForceRebranch, &kvVersion, skipUnreadable)
	if err != nil {
		return nil, fmt.Errorf("merge: %w", err)
	}

	s := DB{
		readonly:         opts.ReadOnly,
		blobClient:       client,
		persist:          nodePersist,
		root:             rootPersist,
		merged:           mergedPersist,
		cfg:              &cfg,
		crdt:             *tree,
		kvVersion:        kvVersion,
		mergedRoots:      mergedRoots,
		unmergeableRoots: unmergeableRoots,
	}
	if !opts.ReadOnly {
		_, err := s.Commit(ctx)
		if err != nil {
			return nil, fmt.Errorf("save merged root: %w", err)
		}
		runtime.SetFinalizer(&s, func(s *DB) {
			if s.IsDirty() {
				panic(fmt.Sprintf("dirty tree should have been Commit()ted or Cancel()ed; path %s", cfg.Storage.Prefix))
			}
		})
	}
	return &s, nil
}

func (spc *AzureBlobStorageInfo) fixPrefix() *AzureBlobStorageInfo {
	if len(spc.Prefix) > 0 && !strings.HasSuffix(spc.Prefix, "/") {
		spc.Prefix += "/"
	}
	return spc
}

func (spc AzureBlobStorageInfo) toPersist(
	client AzureBlobClient,
	suffix string,
) *azurePersist {
	return &azurePersist{
		Client:        client,
		ServiceURL:    spc.ServiceURL,
		ContainerName: spc.ContainerName,
		Prefix:        spc.Prefix + suffix,
	}
}

func (spc AzureBlobStorageInfo) toPersistEncrypt(
	client AzureBlobClient,
	suffix string,
	encryptor Encryptor,
) *persistEncryptor {
	p := spc.toPersist(client, suffix)
	if encryptor == nil {
		encryptor = &noEncryption{}
	}
	return &persistEncryptor{encryptor, p}
}

type noEncryption struct{}

func (p *noEncryption) Encrypt(path string, value []byte) ([]byte, error) { return value, nil }
func (p *noEncryption) Decrypt(path string, value []byte) ([]byte, error) { return value, nil }

type persistEncryptor struct {
	encryptor Encryptor
	*azurePersist
}

func (e *persistEncryptor) Store(ctx context.Context, path string, value []byte) error {
	encrypted, err := e.encryptor.Encrypt(path, value)
	if err != nil {
		return err
	}
	return e.azurePersist.Store(ctx, path, encrypted)
}
func (e *persistEncryptor) Load(ctx context.Context, path string) ([]byte, error) {
	value, err := e.azurePersist.Load(ctx, path)
	if err != nil {
		return nil, err
	}
	return e.encryptor.Decrypt(path, value)
}
func (e *persistEncryptor) NodeURLPrefix() string {
	return e.azurePersist.NodeURLPrefix()
}

func listRoots(
	ctx context.Context,
	client AzureBlobClient,
	rootPersist *azurePersist,
) ([]string, error) {
	roots, err := listObjects(ctx, client, rootPersist.ContainerName, rootPersist.Prefix)
	if err != nil {
		return nil, fmt.Errorf("azure blob list: %w", err)
	}
	return roots, nil
}

func mergeRoots(
	ctx context.Context,
	roots []string,
	cfg Config,
	crdtConfig crdt.Config,
	persists []mast.Persist,
	when time.Time,
	forceRebranch bool,
	maxVersion *int,
	skipUnreadable bool,
) (*crdt.Tree, map[string][]byte, int, error) {
	var err error

	// Even if we can't merge all the roots, due to errors, it's nice to make some
	// progress merging what we can. In order to not be persistently blocked by a
	// lowest-named root, we randomize the order.
	roots = append([]string{}, roots...)
	rand.Shuffle(len(roots), func(i, j int) {
		roots[i], roots[j] = roots[j], roots[i]
	})

	mergedRoots := make(map[string][]byte, len(roots))
	var tree *crdt.Tree
	for _, key := range roots {
		root, rootBytes, err := loadRootFromAny(ctx, persists, key)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("load %v: %w", key, err)
		}
		if root == nil {
			if skipUnreadable {
				continue
			}
			return nil, nil, 0, fmt.Errorf("load %v: not found", key)
		}
		if root.KVVersion > *maxVersion {
			*maxVersion = root.KVVersion
		}
		graft, err := crdt.Load(ctx, crdtConfig, &key, *root)
		if err != nil {
			if isBlobNotFound(err) && skipUnreadable {
				if cfg.LogFunc != nil {
					cfg.LogFunc(fmt.Sprintf("skipping merge for deleted root or parent in %v: %v", key, err))
				}
				continue
			}
			if cfg.LogFunc != nil {
				cfg.LogFunc(fmt.Sprintf("skipping merge for un-crdt.Load()able root %v: %v", key, err))
			}
			return nil, nil, 0, err
		}
		if tree == nil && (!forceRebranch || cfg.BranchFactor == graft.Mast.BranchFactor()) {
			tree = graft
			tree.Source = nil
			tree.MergeSources = []string{key}
		} else {
			if !forceRebranch && tree.Mast.BranchFactor() != graft.Mast.BranchFactor() {
				return nil, nil, 0, fmt.Errorf(
					"cannot merge roots with varying branch factors, %d and %d, without OpenOptions.ForceRebranch",
					tree.Mast.BranchFactor(),
					graft.Mast.BranchFactor())
			}
			if tree == nil {
				tree, err = crdt.Load(ctx, crdtConfig, nil, emptyRoot(when, cfg.BranchFactor, crdtConfig))
				if err != nil {
					return nil, nil, 0, fmt.Errorf("new root: %w", err)
				}
			}

			newTree, err := tree.Clone(ctx)
			if err != nil {
				if cfg.LogFunc != nil && skipUnreadable {
					cfg.LogFunc(fmt.Sprintf("skipping merge un-cloneable tree %v: %v", key, err))
				}
				continue
			}
			err = newTree.Merge(ctx, graft)
			if _, ok := err.(crdt.MergeError); ok {
				return nil, nil, 0, err
			}
			if err != nil {
				if cfg.LogFunc != nil && skipUnreadable {
					cfg.LogFunc(fmt.Sprintf("skipping merge un-cloneable tree %v: %v", key, err))
				}
				continue
			}
			tree = newTree
		}
		mergedRoots[key] = rootBytes
	}

	unmergedRoots := len(roots) - len(mergedRoots)

	if tree == nil {
		root := emptyRoot(when, cfg.BranchFactor, crdtConfig)
		tree, err = crdt.Load(ctx, crdtConfig, nil, root)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("new root: %w", err)
		}
		*maxVersion = root.KVVersion
	} else {
		if len(mergedRoots) == 1 {
			tree.Source = getFirstKey(mergedRoots)
		}
		tree.Created = &when
	}

	return tree, mergedRoots, unmergedRoots, nil
}

func loadRootFromAny(ctx context.Context, persist []mast.Persist, key string) (*crdt.Root, []byte, error) {
	for i := range persist {
		root, rootBytes, err := loadRoot(ctx, persist[i], key)
		if err != nil {
			if isBlobNotFound(err) {
				continue
			}
			return nil, nil, fmt.Errorf("%s: %w", persist[i].NodeURLPrefix(), err)
		}
		return root, rootBytes, nil
	}
	return nil, nil, nil
}

func emptyRoot(when time.Time, branchFactor uint, crdtConfig crdt.Config) crdt.Root {
	empty := crdt.NewRoot(when, branchFactor)
	if crdtConfig.CustomMerge != nil {
		empty.MergeMode = crdt.MergeModeCustom
	} else if crdtConfig.OnConflictMerged != nil {
		empty.MergeMode = crdt.MergeModeCustomLWW
	}
	empty.NodeFormat = crdtConfig.MastNodeFormat
	empty.KVVersion = 1
	return empty
}

func loadRoot(ctx context.Context, persist mast.Persist, key string) (*crdt.Root, []byte, error) {
	rootBytes, err := persist.Load(ctx, key)
	if err != nil {
		return nil, nil, fmt.Errorf("load %s: %w", key, err)
	}
	var root crdt.Root
	jsonErr := json.Unmarshal(rootBytes, &root)
	if jsonErr != nil {
		err = unmarshalGob(rootBytes, &root)
		if err != nil {
			return nil, nil, fmt.Errorf("could not unmarshal as either json: %v, or unmarshal as gob: %w", jsonErr, err)
		}
	}
	return &root, rootBytes, nil
}

// Commit ensures any Set() entries become accessible on subsequent Open()s.
func (s *DB) Commit(ctx context.Context) (*string, error) {
	if !s.IsDirty() && !s.tombstoned && (s.crdt.Source != nil && len(s.crdt.MergeSources) <= 1 ||
		s.crdt.Source == nil && len(s.crdt.MergeSources) == 0) {
		return s.crdt.Source, nil
	}
	if s.readonly {
		return nil, ErrReadOnly
	}
	root, err := s.crdt.MakeRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("mast makeroot: %w", err)
	}
	root.KVVersion = s.kvVersion
	var rootBytes []byte
	switch s.kvVersion {
	case 0:
		rootBytes, err = marshalGob(root)
		if err != nil {
			return nil, fmt.Errorf("marshal root: gob: %w", err)
		}
	case 1:
		rootBytes, err = json.Marshal(root)
		if err != nil {
			return nil, fmt.Errorf("marshal root: json: %w", err)
		}
	default:
		return nil, fmt.Errorf("unhandled kv version: %v", root.KVVersion)
	}

	hashBytes := blake2b.Sum256(rootBytes)
	crTime := big.NewInt(root.Created.Unix()).Text(62)
	hash := big.NewInt(0).SetBytes(hashBytes[:12]).Text(62)
	name := fmt.Sprintf("%s%06s_%s", s.cfg.CustomRootPrefix, crTime, hash)
	err = s.root.Store(ctx, name, rootBytes)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	s.moveMergedRoots(ctx, name, s.mergedRoots)
	s.mergedRoots = map[string][]byte{name: rootBytes}
	s.crdt.MergeSources = []string{name}
	s.crdt.Source = &name
	s.tombstoned = false
	return &name, nil
}

func (s DB) listRoots(ctx context.Context) ([]string, error) {
	return listObjects(ctx, s.blobClient, s.root.ContainerName, s.root.Prefix)
}

func (s DB) listMergedRoots(ctx context.Context) ([]string, error) {
	return listObjects(ctx, s.blobClient, s.merged.ContainerName, s.merged.Prefix)
}

func (s DB) listNodes(ctx context.Context) ([]string, error) {
	p := s.persist.(*persistEncryptor)
	return listObjects(ctx, s.blobClient, p.ContainerName, p.Prefix)
}

func listObjects(ctx context.Context, c AzureBlobClient, containerName, prefix string) ([]string, error) {
	blobs, err := c.ListBlobs(ctx, containerName, prefix)
	if err != nil {
		return nil, fmt.Errorf("azure blob: %w", err)
	}
	res := make([]string, 0, len(blobs))
	for _, blob := range blobs {
		res = append(res, strings.TrimPrefix(blob, prefix))
	}
	return res, nil
}

// Clone returns an independent database, with the same entries and uncommitted values as its
// source. Clones don't duplicate nodes that can be shared.
func (s *DB) Clone(ctx context.Context) (*DB, error) {
	kvCopy := *s
	cfgCopy := *s.cfg
	kvCopy.cfg = &cfgCopy
	crdtCopy, err := s.crdt.Clone(ctx)
	if err != nil {
		return nil, err
	}
	kvCopy.crdt = *crdtCopy
	return &kvCopy, nil
}

// Cancel indicates you don't want any of the previously-set entries persisted.
func (s *DB) Cancel() {
	runtime.SetFinalizer(s, nil)
}

func getFirstKey(m map[string][]byte) *string {
	for k := range m {
		return &k
	}
	return nil
}

type rootGraph map[string]*crdt.Root

func getFirst(m map[string]struct{}) (string, bool) {
	for k := range m {
		return k, true
	}
	return "", false
}

func (s DB) loadRootGraph(ctx context.Context) (rootGraph, error) {
	g := rootGraph{}
	todo := map[string]struct{}{}
	for _, rootName := range s.crdt.MergeSources {
		todo[rootName] = struct{}{}
	}
	persists := []mast.Persist{s.merged, s.root}
	for {
		rootName, ok := getFirst(todo)
		if !ok {
			break
		}
		root, _, err := loadRootFromAny(ctx, persists, rootName)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", rootName, err)
		}
		if root == nil {
			// un-merged (current), or delayed root
			delete(todo, rootName)
			continue
		}
		g[rootName] = root
		for _, parentName := range root.MergeSources {
			if _, ok := g[parentName]; !ok {
				todo[parentName] = struct{}{}
			}
		}
		delete(todo, rootName)
	}
	return g, nil
}

type dependentRoots map[string]map[string]*crdt.Root

func getDependents(mergedRoots rootGraph) dependentRoots {
	dependents := make(map[string]map[string]*crdt.Root, len(mergedRoots))
	for name, root := range mergedRoots {
		for _, source := range root.MergeSources {
			if dependentsForSource, ok := dependents[source]; ok {
				dependentsForSource[name] = root
			} else {
				dependents[source] = map[string]*crdt.Root{name: root}
			}
		}
	}
	return dependents
}

func (s *DB) moveMergedRoots(ctx context.Context, newRoot string, mergedRoots map[string][]byte) {
	for key, mergedRoot := range mergedRoots {
		if newRoot == key {
			continue
		}
		err := s.merged.Store(ctx, key, mergedRoot)
		if err != nil {
			// LOG return nil, fmt.Errorf("store merged root: %w", err)
			return
		}
		err = s.blobClient.DeleteBlob(ctx, s.root.ContainerName, s.root.Prefix+key)
		if err != nil {
			// LOG return nil, fmt.Errorf("delete merged root: azure blob: %w", err)
			return
		}
	}
}

func (s *DB) getHistoricRootsAndNodes(
	ctx context.Context,
	olderThan time.Time,
	logFunc func(string),
) (roots, nodes []string, err error) {
	rootCacheByName, err := s.loadRootGraph(ctx)
	if err != nil {
		return nil, nil, err
	}
	candidateRoots := dependentRoots{}
	parentToChildren := getDependents(rootCacheByName)
	for parent, children := range parentToChildren {
		tooNew := false
		for _, childRoot := range children {
			if childRoot.Created == nil || childRoot.Created.After(olderThan) {
				tooNew = true
				break
			}
		}
		if !tooNew {
			candidateRoots[parent] = children
		}
	}
	candidateBlocks := make(map[string]int) // track root that can be deleted too
	for parentName, children := range candidateRoots {
		parentRoot, ok := rootCacheByName[parentName]
		if !ok {
			continue
		}
		parent, err := crdt.Load(ctx, s.crdt.Config, &parentName, *parentRoot)
		if err != nil {
			if logFunc != nil {
				logFunc(fmt.Sprintf("error loading parent %v: %v\n", parentRoot, err))
			}
			continue
		}
		for childName, childRoot := range children {
			child, err := crdt.Load(ctx, s.crdt.Config, &childName, *childRoot)
			if err != nil {
				if logFunc != nil {
					logFunc(fmt.Sprintf("error loading child %v: %v\n", childName, err))
				}
				continue
			}
			err = child.Mast.DiffLinks(ctx, parent.Mast,
				func(removed bool, link interface{}) (bool, error) {
					if removed {
						if ls, ok := link.(string); ok {
							candidateBlocks[ls] = 1
						}
					}
					return true, nil
				})
			if err != nil {
				if logFunc != nil {
					logFunc(fmt.Sprintf("error diffing %s: %v\n", childName, err))
				}
			}
		}
	}
	nodes = make([]string, 0, len(candidateBlocks))
	for k := range candidateBlocks {
		nodes = append(nodes, k)
	}
	roots = make([]string, 0, len(candidateRoots))
	for k := range candidateRoots {
		roots = append(roots, k)
	}
	return roots, nodes, nil
}

// IsDirty returns true if there are entries in memory that haven't been Commit()ted.
func (s DB) IsDirty() bool {
	return s.tombstoned || s.crdt.IsDirty()
}

// Set puts a new value in memory. If the database already has a value later than "when", this does
// nothing. Any new values are buffered in memory until Commit()ted or Cancel()ed.
func (s *DB) Set(ctx context.Context, when time.Time, key interface{}, value interface{}) error {
	if s.readonly {
		return ErrReadOnly
	}
	return s.crdt.Set(ctx, when, key, value)
}

// Get retrieves the value for the given key. value must be a pointer that to
// which an Config.ValuesLike can be assigned, OR a pointer to a crdt.Value,
// in which case the CRDT value metadata will be included.
func (s *DB) Get(ctx context.Context, key interface{}, value interface{}) (bool, error) {
	return s.crdt.Get(ctx, key, value)
}

// IsTombstoned returns true if Tombstone() was invoked and committed
// since the last RemoveTombstones().
func (s *DB) IsTombstoned(ctx context.Context, key interface{}) (bool, error) {
	return s.crdt.IsTombstoned(ctx, key)
}

// Tombstone sets a persistent record indicating an entry will no longer be accessible, until
// RemoveTombstones() is done. If multiple versions have different values or tombstones for a key,
// they'll eventually get merged into the earliest tombstone. You can set a tombstone even if the
// entry was never set to a value, just to ensure that any future sets are ineffective.
func (s *DB) Tombstone(ctx context.Context, when time.Time, key interface{}) error {
	if s.readonly {
		return ErrReadOnly
	}
	return s.crdt.Tombstone(ctx, when, key)
}

// Height is the number of nodes between a leaf and the root—the number of nodes that need to be retrieved to do a Get() in the worst case.
func (s DB) Height() int {
	return int(s.crdt.Mast.Height())
}

// Size returns the number of entries and tombstones in this tree.
func (s DB) Size() uint64 {
	return s.crdt.Mast.Size()
}

// Diff shows the differences from the the given tree to this one,
// invoking the given callback for each added/removed/changed value.
func (s DB) Diff(
	ctx context.Context,
	from *DB,
	f func(key, myValue, fromValue interface{}) (keepGoing bool, err error),
) error {
	var fromMast *mast.Mast = nil
	if from != nil {
		fromMast = from.crdt.Mast
	}
	err := s.crdt.Mast.DiffIter(ctx, fromMast,
		func(added, removed bool,
			key, addedValue, removedValue interface{},
		) (keepGoing bool, err error) {
			myValue := innerValue(addedValue)
			fromValue := innerValue(removedValue)
			if reflect.DeepEqual(myValue, fromValue) {
				return true, nil
			}
			return f(key, myValue, fromValue)
		})
	return err
}

func innerValue(v interface{}) interface{} {
	if cv, ok := v.(crdtpub.Value); ok {
		if cv.TombstoneSinceEpochNanos != 0 {
			return nil
		}
		return cv.Value
	}
	return nil
}

// RemoveTombstones gets rid of the old tombstones, in the current version. The
// time should be chosen such that there are not going to be any merges for
// affected entries, otherwise merged sets will start new rounds of values for
// the affected entries. For example, if it would be valid for clients to retry
// hour-old requests, 'before' should be at least an hour ago.
func (s *DB) RemoveTombstones(ctx context.Context, before time.Time) error {
	sc, err := s.Clone(ctx)
	if err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	cutoff := before.UnixNano()
	origSize := s.Size()
	err = sc.crdt.Mast.DiffIter(ctx, nil, func(added, removed bool, key, addedValue, removedValue interface{}) (keepGoing bool, err error) {
		cv := addedValue.(crdtpub.Value)
		ts := cv.TombstoneSinceEpochNanos
		if ts == 0 || ts >= cutoff {
			return true, nil
		}
		return true, s.crdt.Mast.Delete(ctx, key, addedValue)
	})
	if err != nil {
		return err
	}
	if s.Size() < origSize {
		s.tombstoned = true
	}
	return nil
}

// DeleteeHistoricalVersionsAndData deletes storage associated with old
// versions, and any nodes from those versions that are no longer necessary for
// reading later versions. Deletion reduces the number of objects in the container
// but means that attempts to Diff(), TraceHistory(), or merge versions before
// the cutoff will fail.
func DeleteHistoricVersions(ctx context.Context, s *DB, before time.Time) error {
	if s.readonly {
		return ErrReadOnly
	}
	roots, nodes, err := s.getHistoricRootsAndNodes(ctx, before, s.cfg.LogFunc)
	if err != nil {
		return fmt.Errorf("get historic roots: %w", err)
	}
	for _, l := range nodes {
		err := s.blobClient.DeleteBlob(ctx, s.persist.(*persistEncryptor).ContainerName, s.persist.(*persistEncryptor).Prefix+l)
		if err != nil {
			return fmt.Errorf("delete node: %s: %w", l, err)
		}
	}
	for _, l := range roots {
		err := s.blobClient.DeleteBlob(ctx, s.merged.ContainerName, s.merged.Prefix+l)
		if err != nil {
			return fmt.Errorf("delete root: %s: %w", l, err)
		}
	}
	// An empty current root is equivalent to a nonexistent root,
	// so we could delete it too if it meets the history requirement.
	if s.crdt.Source != nil && !s.IsDirty() && s.Size() == 0 {
		root, _, err := loadRoot(ctx, s.root, *s.crdt.Source)
		if err == nil && root.Created.Before(before) {
			err := s.blobClient.DeleteBlob(ctx, s.root.ContainerName, s.root.Prefix+*s.crdt.Source)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type dbAndCutoff struct {
	db     *DB
	cutoff int64
}

// TraceHistory invokes the given callback for each historic value of 'key', up
// to the last RemoveTombstones().
func (s *DB) TraceHistory(
	ctx context.Context,
	key interface{},
	after time.Time,
	cb func(when time.Time, value interface{}) (keepGoing bool, err error),
) error {
	round := []dbAndCutoff{{s, math.MaxInt64}}
	for len(round) > 0 {
		nextRound := []dbAndCutoff{}
		for _, r := range round {
			var gv crdtpub.Value
			contains, err := r.db.crdt.Mast.Get(ctx, key, &gv)
			if err != nil {
				return fmt.Errorf("crdt.get: %w", err)
			}
			if !contains || gv.ModEpochNanos >= r.cutoff {
				continue
			}
			when := time.Unix(0, gv.ModEpochNanos)
			if time.Unix(0, gv.ModEpochNanos).Before(after) {
				continue
			}
			keepGoing, err := cb(when, gv.Value)
			if err != nil {
				return fmt.Errorf("cb: %w", err)
			}
			if !keepGoing {
				return nil
			}
			if gv.PreviousRoot == "" {
				continue
			}
			prev, err := Open(ctx, s.blobClient, *s.cfg,
				OpenOptions{
					ReadOnly:     true,
					OnlyVersions: []string{gv.PreviousRoot},
				}, time.Time{})
			if err != nil {
				return fmt.Errorf("open previous root: %s: %w", gv.PreviousRoot, err)
			}
			nextRound = append(nextRound, dbAndCutoff{prev, gv.ModEpochNanos})
			for _, source := range prev.crdt.MergeSources {
				prev, err := Open(ctx, s.blobClient, *s.cfg,
					OpenOptions{
						ReadOnly:     true,
						OnlyVersions: []string{source},
					}, time.Time{})
				if err != nil {
					return fmt.Errorf("open previous source %s: %w", source, err)
				}
				nextRound = append(nextRound, dbAndCutoff{prev, gv.ModEpochNanos})
			}
		}

		trimmed := []dbAndCutoff{}
		queuedOrDone := map[string]bool{}
		for _, r := range round {
			queuedOrDone[r.root()] = true
		}
		for _, r := range nextRound {
			if !queuedOrDone[r.root()] {
				trimmed = append(trimmed, r)
				queuedOrDone[r.root()] = true
			}
		}
		round = trimmed
	}
	return nil
}

func (r *dbAndCutoff) root() string {
	if r.db.crdt.Source == nil {
		return ""
	}
	return *r.db.crdt.Source
}

func (d *DB) BranchFactor() uint {
	return d.crdt.Mast.BranchFactor()
}

type DiffCursor struct {
	*mast.DiffCursor
}

func (d *DB) StartDiff(ctx context.Context, other *DB) (*DiffCursor, error) {
	inner, err := d.crdt.Mast.StartDiff(ctx, other.crdt.Mast)
	if err != nil {
		return nil, err
	}
	return &DiffCursor{
		DiffCursor: inner,
	}, nil
}
func (dc *DiffCursor) NextEntry(ctx context.Context) (mast.Diff, error) {
	inner, err := dc.DiffCursor.NextEntry(ctx)
	if err != nil {
		return mast.Diff{}, err
	}
	if inner.OldValue != nil {
		inner.OldValue = inner.OldValue.(crdtpub.Value).Value
	}
	if inner.NewValue != nil {
		inner.NewValue = inner.NewValue.(crdtpub.Value).Value
	}
	return inner, nil
}

type Cursor struct {
	*mast.Cursor
}

func (d *DB) Cursor(ctx context.Context) (*Cursor, error) {
	inner, err := d.crdt.Mast.Cursor(ctx)
	if err != nil {
		return nil, err
	}
	return &Cursor{
		Cursor: inner,
	}, nil
}

// Get overrides mast.Cursor.Get() to make the crdt.Value wrapping clear.
func (c *Cursor) Get() (interface{}, *crdtpub.Value, bool) {
	innerKey, innerValue, ok := c.Cursor.Get()
	if !ok {
		return nil, nil, false
	}
	v := innerValue.(crdtpub.Value)
	return innerKey, &v, true
}

func (s *DB) Roots() ([]string, error) {
	if !s.readonly && s.IsDirty() {
		return nil, errors.New("db has uncommitted values")
	}
	i := 0
	var roots []string
	for k := range s.mergedRoots {
		if k != "" {
			roots = append(roots, k)
		}
		i++
	}
	sort.Strings(roots)
	return roots, nil
}
