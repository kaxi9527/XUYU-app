package versions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const maxCatalogBytes = 32 << 20

var (
	ErrConflict        = errors.New("作品记录已更新，请重新读取后再操作")
	ErrOperationReused = errors.New("同一操作标识不能用于不同内容")
	ErrCorrupt         = errors.New("作品记录无法读取；原记录未修改")
)

// Store is independent of the legacy CSV index. Constructing or reading it never
// creates storage. Only typed commands added within this package may call update.
type Store struct {
	directory string
	replace   func(string, string) error
	gate      chan struct{}
}

func NewStore(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" || !filepath.IsAbs(directory) {
		return nil, errors.New("作品版本目录必须是完整路径")
	}
	clean := filepath.Clean(directory)
	ancestor := clean
	for {
		fi, err := os.Lstat(ancestor)
		if err == nil {
			if ancestor == clean && fi.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("作品版本目录不能是链接")
			}
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(ancestor, clean)
			if err != nil {
				return nil, err
			}
			return &Store{directory: filepath.Join(resolved, rel), replace: os.Rename, gate: make(chan struct{}, 1)}, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return nil, err
		}
		ancestor = next
	}
}

// directoryReady rejects links introduced after construction, including parents.
func (s *Store) directoryReady(create bool) (bool, error) {
	var paths []string
	for p := s.directory; ; p = filepath.Dir(p) {
		paths = append(paths, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return false, nil
			}
			if err = os.Mkdir(p, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return false, err
			}
			fi, err = os.Lstat(p)
		}
		if err != nil {
			return false, err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("作品版本目录包含链接或非文件夹")
		}
	}
	return true, nil
}

type contextReader struct {
	context context.Context
	reader  io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func (s *Store) read(ctx context.Context) (Catalog, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Catalog{}, nil, err
	}
	ready, err := s.directoryReady(false)
	if err != nil {
		return Catalog{}, nil, err
	}
	if !ready {
		return emptyCatalog(), nil, nil
	}
	path := filepath.Join(s.directory, "catalog.json")
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyCatalog(), nil, nil
	}
	if err != nil {
		return Catalog{}, nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxCatalogBytes {
		return Catalog{}, nil, fmt.Errorf("%w：文件类型或大小异常", ErrCorrupt)
	}
	f, err := os.Open(path)
	if err != nil {
		return Catalog{}, nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(fi, opened) {
		return Catalog{}, nil, ErrConflict
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, maxCatalogBytes+1))
	if err != nil {
		return Catalog{}, nil, err
	}
	if len(data) > maxCatalogBytes {
		return Catalog{}, nil, fmt.Errorf("%w：超过读取上限", ErrCorrupt)
	}
	after, err := f.Stat()
	if err != nil || !sameFileState(opened, after) {
		return Catalog{}, nil, ErrConflict
	}
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&catalog); err != nil {
		return Catalog{}, nil, fmt.Errorf("%w：%v", ErrCorrupt, err)
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return Catalog{}, nil, fmt.Errorf("%w：记录包含额外内容", ErrCorrupt)
	}
	if err = catalog.Validate(); err != nil {
		return Catalog{}, nil, fmt.Errorf("%w：%v", ErrCorrupt, err)
	}
	return catalog, data, nil
}

func (s *Store) Read(ctx context.Context) (Catalog, error) { c, _, err := s.read(ctx); return c, err }

func (s *Store) update(ctx context.Context, expectedRevision uint64, operationID, action string, request any, change func(*Catalog) ([]string, error)) (Receipt, error) {
	if !writesSupported() {
		return Receipt{}, errors.New("此平台尚未支持作品版本的安全并发写入")
	}
	if strings.TrimSpace(operationID) == "" || len(operationID) > 128 {
		return Receipt{}, errors.New("缺少有效的操作标识")
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return Receipt{}, ctx.Err()
	}
	payload, err := json.Marshal(struct {
		Action  string
		Request any
	}{action, request})
	if err != nil {
		return Receipt{}, err
	}
	digest := sha256.Sum256(payload)
	requestHash := hex.EncodeToString(digest[:])
	if _, err = s.directoryReady(true); err != nil {
		return Receipt{}, err
	}
	lockPath := filepath.Join(s.directory, "catalog.lock")
	if fi, e := os.Lstat(lockPath); e == nil && !fi.Mode().IsRegular() {
		return Receipt{}, errors.New("作品版本锁不是普通文件")
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return Receipt{}, e
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Receipt{}, err
	}
	defer lock.Close()
	if err = lockContext(ctx, lock); err != nil {
		return Receipt{}, err
	}
	defer unlock(lock)
	opened, e := lock.Stat()
	current, e2 := os.Lstat(lockPath)
	if e != nil || e2 != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return Receipt{}, ErrConflict
	}
	catalog, original, err := s.read(ctx)
	if err != nil {
		return Receipt{}, err
	}
	for _, receipt := range catalog.Operations {
		if receipt.OperationID == operationID {
			if receipt.RequestHash != requestHash {
				return Receipt{}, ErrOperationReused
			}
			return receipt, nil
		}
	}
	if catalog.Revision != expectedRevision {
		return Receipt{}, ErrConflict
	}
	if catalog.Revision >= (1<<53)-1 {
		return Receipt{}, errors.New("作品记录修订号达到上限")
	}
	oldVersions := append([]Version(nil), catalog.Versions...)
	oldConfirmations := append([]Confirmation(nil), catalog.Confirmations...)
	oldOperations := append([]Receipt(nil), catalog.Operations...)
	for i := range oldOperations {
		if oldOperations[i].EntityIDs != nil {
			oldOperations[i].EntityIDs = append([]string{}, oldOperations[i].EntityIDs...)
		}
	}
	entityIDs, err := change(&catalog)
	if err != nil {
		return Receipt{}, err
	}
	// Content identities and confirmations are append-only even when a version is
	// explicitly regrouped. Mutation callbacks cannot rewrite old operation receipts.
	byID := map[string]Version{}
	for _, v := range catalog.Versions {
		byID[v.ID] = v
	}
	for _, old := range oldVersions {
		now, ok := byID[old.ID]
		if !ok || now.Content != old.Content || now.RegisteredAt != old.RegisteredAt {
			return Receipt{}, errors.New("已有版本内容身份不能改写或删除")
		}
	}
	if len(catalog.Confirmations) < len(oldConfirmations) || !reflect.DeepEqual(oldConfirmations, append([]Confirmation(nil), catalog.Confirmations[:len(oldConfirmations)]...)) {
		return Receipt{}, errors.New("历史确认不能改写")
	}
	if !reflect.DeepEqual(oldOperations, append([]Receipt(nil), catalog.Operations...)) {
		return Receipt{}, errors.New("历史操作回执不能改写")
	}
	catalog.Revision = expectedRevision + 1
	receipt := Receipt{OperationID: operationID, RequestHash: requestHash, Action: action, Revision: catalog.Revision, EntityIDs: entityIDs}
	catalog.Operations = append(catalog.Operations, receipt)
	if err = catalog.Validate(); err != nil {
		return Receipt{}, err
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return Receipt{}, err
	}
	data = append(data, '\n')
	if len(data) > maxCatalogBytes {
		return Receipt{}, errors.New("作品记录达到 32 MiB 上限；原记录未修改")
	}
	if err = ctx.Err(); err != nil {
		return Receipt{}, err
	}
	committed, err := s.writeAtomic(ctx, data, original)
	if err != nil {
		if committed {
			return receipt, fmt.Errorf("记录已替换但目录同步失败，请用原操作标识重试确认：%w", err)
		}
		return Receipt{}, err
	}
	return receipt, nil
}

func (s *Store) writeAtomic(ctx context.Context, data, original []byte) (bool, error) {
	if _, err := s.directoryReady(false); err != nil {
		return false, err
	}
	parent, err := os.Stat(s.directory)
	if err != nil {
		return false, err
	}
	f, err := os.CreateTemp(s.directory, ".catalog-*.tmp")
	if err != nil {
		return false, err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return false, err
	}
	if err = f.Sync(); err != nil {
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	// A non-cooperating external editor is not protected by our lock. Recheck
	// after writing the temporary file, immediately before replacing the catalog.
	_, latest, err := s.read(ctx)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(original, latest) {
		return false, ErrConflict
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	current, err := os.Lstat(s.directory)
	if err != nil || !current.IsDir() || !os.SameFile(parent, current) {
		return false, ErrConflict
	}
	if err = s.replace(name, filepath.Join(s.directory, "catalog.json")); err != nil {
		return false, err
	}
	dir, err := os.Open(s.directory)
	if err != nil {
		return true, err
	}
	defer dir.Close()
	return true, dir.Sync()
}
