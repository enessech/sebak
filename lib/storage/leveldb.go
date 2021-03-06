package sebakstorage

import (
	"encoding/json"
	"errors"
	"fmt"

	"boscoin.io/sebak/lib/common"
	"github.com/syndtr/goleveldb/leveldb"
	leveldbIterator "github.com/syndtr/goleveldb/leveldb/iterator"
	leveldbOpt "github.com/syndtr/goleveldb/leveldb/opt"
	leveldbStorage "github.com/syndtr/goleveldb/leveldb/storage"
	leveldbUtil "github.com/syndtr/goleveldb/leveldb/util"
)

type LevelDBCore interface {
	Has([]byte, *leveldbOpt.ReadOptions) (bool, error)
	Get([]byte, *leveldbOpt.ReadOptions) ([]byte, error)
	NewIterator(*leveldbUtil.Range, *leveldbOpt.ReadOptions) leveldbIterator.Iterator
	Put([]byte, []byte, *leveldbOpt.WriteOptions) error
	Write(*leveldb.Batch, *leveldbOpt.WriteOptions) error
	Delete([]byte, *leveldbOpt.WriteOptions) error
}

type LevelDBBackend struct {
	DB *leveldb.DB

	core LevelDBCore
}

func (st *LevelDBBackend) Init(config *Config) (err error) {
	var sto leveldbStorage.Storage
	if config.Scheme == "memory" {
		sto = leveldbStorage.NewMemStorage()
	} else if config.Scheme == "file" {
		if sto, err = leveldbStorage.OpenFile(config.Path, false); err != nil {
			return
		}
	}

	var db *leveldb.DB
	if db, err = leveldb.Open(sto, nil); err != nil {
		return
	}

	st.DB = db
	st.core = db

	return
}

func (st *LevelDBBackend) Close() error {
	return st.DB.Close()
}

func (st *LevelDBBackend) OpenTransaction() (*LevelDBBackend, error) {
	_, ok := st.core.(*leveldb.Transaction)
	if ok {
		return nil, errors.New("this is already *leveldb.Transaction")
	}

	transaction, err := st.core.(*leveldb.DB).OpenTransaction()
	if err != nil {
		return nil, err
	}

	return &LevelDBBackend{
		DB:   st.DB,
		core: transaction,
	}, nil
}

func (st *LevelDBBackend) Discard() error {
	ts, ok := st.core.(*leveldb.Transaction)
	if !ok {
		return errors.New("this is not *leveldb.Transaction")
	}

	ts.Discard()
	return nil
}

func (st *LevelDBBackend) Commit() error {
	ts, ok := st.core.(*leveldb.Transaction)
	if !ok {
		return errors.New("this is not *leveldb.Transaction")
	}

	return ts.Commit()
}

func (st *LevelDBBackend) makeKey(key string) []byte {
	return []byte(key)
}

func (st *LevelDBBackend) Has(k string) (bool, error) {
	return st.core.Has(st.makeKey(k), nil)
}

func (st *LevelDBBackend) GetRaw(k string) (b []byte, err error) {
	var exists bool
	if exists, err = st.Has(k); !exists || err != nil {
		if !exists {
			err = fmt.Errorf("key, '%s' does not exists", k)
		}
		return
	}

	b, err = st.core.Get(st.makeKey(k), nil)

	return
}

func (st *LevelDBBackend) Get(k string, i interface{}) (err error) {
	var b []byte
	if b, err = st.GetRaw(k); err != nil {
		return
	}

	if err = json.Unmarshal(b, &i); err != nil {
		return
	}

	return
}

func (st *LevelDBBackend) New(k string, v interface{}) (err error) {
	var encoded []byte
	serializable, ok := v.(sebakcommon.Serializable)
	if ok {
		encoded, err = serializable.Serialize()
	} else {
		encoded, err = sebakcommon.EncodeJSONValue(v)
	}
	if err != nil {
		return
	}

	var exists bool
	if exists, err = st.Has(k); exists || err != nil {
		if exists {
			err = fmt.Errorf("key, '%s' already exists", k)
		}
		return
	}

	err = st.core.Put(st.makeKey(k), encoded, nil)

	return
}

func (st *LevelDBBackend) News(vs ...Item) (err error) {
	if len(vs) < 1 {
		err = errors.New("empty values")
		return
	}

	var exists bool
	for _, v := range vs {
		if exists, err = st.Has(v.Key); exists || err != nil {
			if exists {
				err = fmt.Errorf("found existing key, '%s'", v.Key)
			}
			return
		}
	}

	batch := new(leveldb.Batch)
	for _, v := range vs {
		var encoded []byte
		if encoded, err = sebakcommon.EncodeJSONValue(v); err != nil {
			return
		}

		batch.Put(st.makeKey(v.Key), encoded)
	}

	err = st.core.Write(batch, nil)

	return
}

func (st *LevelDBBackend) Set(k string, v interface{}) (err error) {
	var encoded []byte
	if encoded, err = sebakcommon.EncodeJSONValue(v); err != nil {
		return
	}

	var exists bool
	if exists, err = st.Has(k); !exists || err != nil {
		if !exists {
			err = fmt.Errorf("key, '%s' does not exists", k)
		}
		return
	}

	err = st.core.Put(st.makeKey(k), encoded, nil)

	return
}

func (st *LevelDBBackend) Sets(vs ...Item) (err error) {
	if len(vs) < 1 {
		err = errors.New("empty values")
		return
	}

	var exists bool
	for _, v := range vs {
		if exists, err = st.Has(v.Key); !exists || err != nil {
			if !exists {
				err = fmt.Errorf("not found key, '%s'", v.Key)
			}
			return
		}
	}

	batch := new(leveldb.Batch)
	for _, v := range vs {
		var encoded []byte
		if encoded, err = sebakcommon.EncodeJSONValue(v); err != nil {
			return
		}

		batch.Put(st.makeKey(v.Key), encoded)
	}

	err = st.core.Write(batch, nil)

	return
}

func (st *LevelDBBackend) Remove(k string) (err error) {
	var exists bool
	if exists, err = st.Has(k); !exists || err != nil {
		if !exists {
			err = fmt.Errorf("key, '%s' does not exists", k)
		}
		return
	}

	err = st.core.Delete(st.makeKey(k), nil)

	return
}

func (st *LevelDBBackend) GetIterator(prefix string, reverse bool) (func() (IterItem, bool), func()) {
	var dbRange *leveldbUtil.Range
	if len(prefix) > 0 {
		dbRange = leveldbUtil.BytesPrefix(st.makeKey(prefix))
	}

	iter := st.core.NewIterator(dbRange, nil)

	var funcNext func() bool
	var hasUnsent bool
	if reverse {
		if !iter.Last() {
			iter.Release()
			return (func() (IterItem, bool) { return IterItem{}, false }), (func() {})
		}
		funcNext = iter.Prev
		hasUnsent = true
	} else {
		funcNext = iter.Next
		hasUnsent = false
	}

	var n int64
	return (func() (IterItem, bool) {
			if hasUnsent {
				hasUnsent = false
				return IterItem{N: n, Key: iter.Key(), Value: iter.Value()}, true
			}

			if !funcNext() {
				iter.Release()
				return IterItem{}, false
			}

			n++
			return IterItem{N: n, Key: iter.Key(), Value: iter.Value()}, true
		}),
		(func() {
			iter.Release()
		})
}
