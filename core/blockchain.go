package core

import (
	"encoding/hex"
	lru "github.com/hashicorp/golang-lru"
	log "github.com/sirupsen/logrus"
	"github.com/syndtr/goleveldb/leveldb/errors"
	"go-chronos/common"
	"go-chronos/utils"
	karmem "karmem.org/golang"
	"sync"
	"time"
)

const (
	maxBlockCache       = 1024
	maxTransactionCache = 32768
)

var (
	blockChainOnce sync.Once
	blockInst      *blockChain
)

type blockChain struct {
	dbWriterQueue chan *common.Block

	blockHeightMap *lru.Cache
	blockCache     *lru.Cache
	txCache        *lru.Cache

	latestBlock  *common.Block
	latestHeight int
	latestLock   sync.RWMutex
}

func GetBlockChainInst() *blockChain {
	blockChainOnce.Do(func() {
		blockCache, err := lru.New(maxBlockCache)

		if err != nil {
			log.WithField("error", err).Debugln("Create block cache failed")
			return
		}

		txCache, err := lru.New(maxTransactionCache)

		if err != nil {
			log.WithField("error", err).Debugln("Create transaction cache failed.")
			return
		}

		blockHeightMap, err := lru.New(maxBlockCache)

		if err != nil {
			log.WithField("error", err).Debugln("Create block map cache failed.")
			return
		}

		blockInst = &blockChain{
			dbWriterQueue:  make(chan *common.Block),
			blockHeightMap: blockHeightMap,
			blockCache:     blockCache,
			txCache:        txCache,
			latestBlock:    nil,
			latestHeight:   -1,
		}
	})
	return blockInst
}

// PackageNewBlock 打包新的区块，传入交易序列
func (bc *blockChain) PackageNewBlock(txs []common.Transaction) (*common.Block, error) {
	merkleRoot := BuildMerkleTree(txs)
	block := common.Block{
		Header: common.BlockHeader{
			Timestamp:     uint64(time.Now().UnixMilli()),
			PrevBlockHash: [32]byte{},
			BlockHash:     [32]byte{},
			MerkleRoot:    [32]byte(merkleRoot),
			Height:        0,
		},
		Transactions: txs,
	}
	return &block, nil
}

func (bc *blockChain) GetLatestBlock() (*common.Block, error) {
	if bc.latestBlock != nil {
		return bc.latestBlock, nil
	}

	db := utils.GetLevelDBInst()
	latestBlockHash, err := db.Get([]byte("latest"))
	if err != nil {
		log.WithField("error", err).Errorln("Get latest block hash failed.")
		return nil, err
	}

	byteBlockData, err := db.Get(utils.BlockHash2DBKey(common.Hash(latestBlockHash)))

	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
			"hash":  hex.EncodeToString(latestBlockHash),
		}).Errorln("Get latest block data failed.")
		return nil, err
	}

	block, err := utils.DeserializeBlock(byteBlockData)

	if err != nil {
		log.WithField("error", err).Errorln("Deserialize block failed.")
		return nil, err
	}

	return block, nil
}

func (bc *blockChain) GetBlockByHash(hash *common.Hash) (*common.Block, error) {
	// 先查询缓存是否命中
	value, hit := bc.blockCache.Get(hash)

	if hit {
		// 命中缓存， 直接返回区块
		return value.(*common.Block), nil
	}

	blockKey := utils.BlockHash2DBKey(*hash)
	db := utils.GetLevelDBInst()
	// todo: 需要一个命名规范
	byteBlockData, err := db.Get(blockKey)

	if err != nil {
		log.WithField("error", err).Debugln("Get block data in database failed.")
		return nil, err
	}
	block, _ := utils.DeserializeBlock(byteBlockData)
	bc.writeCache(block)

	return block, nil
}

func (bc *blockChain) GetBlockByHeight(height int) (*common.Block, error) {
	// 先判断一下高度
	if height > bc.latestHeight {
		return nil, errors.New("Block height error.")
	}

	// 查询缓存里面是否有区块的信息
	value, ok := bc.blockHeightMap.Get(height)
	if ok {
		blockHash := value.(*common.Hash)
		block, err := bc.GetBlockByHash(blockHash)

		if err != nil {
			log.WithField("error", err).Errorln("Get block by hash failed.")
			return nil, err
		}

		return block, nil
	}

	// 查询数据库
	db := utils.GetLevelDBInst()
	heightDBKey := utils.BlockHeight2DBKey(height)
	blockHash, err := db.Get(heightDBKey)

	if err != nil {
		log.WithField("error", err).Errorln("Get block hash with height failed.")
		return nil, err
	}

	hash := common.Hash(blockHash)
	block, err := bc.GetBlockByHash(&hash)

	if err != nil {
		log.WithField("error", err).Errorln("Get block by hash failed.")
		return nil, err
	}

	return block, nil
}

func (bc *blockChain) writeCache(block *common.Block) {
	bc.blockHeightMap.Add(block.Header.Height, block.Header.BlockHash)
	bc.blockCache.Add(block.Header.BlockHash, block)
}

// todo: 这里作为公开函数只是为了测试
func (bc *blockChain) InsertBlock(block *common.Block) error {
	var err error
	count := len(block.Transactions)
	keys := make([][]byte, count+1)
	values := make([][]byte, count+1)

	values[0], err = utils.SerializeBlock(block)
	if err != nil {
		return err
	}
	//fmt.Printf("Data size %d bytes.", len(values[0]))

	bc.latestLock.RLock()
	bc.latestBlock = block
	bc.latestHeight = int(block.Header.Height)
	bc.latestLock.RUnlock()
	bc.writeCache(block)

	db := utils.GetLevelDBInst()

	for idx := range block.Transactions {
		// todo： 或许需要校验一下交易是否合法
		tx := block.Transactions[idx]

		txWriter := karmem.NewWriter(1024)
		keys[idx+1] = append([]byte("tx#"), tx.Body.Hash[:]...)
		_, err := tx.WriteAsRoot(txWriter)

		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"hash":  hex.EncodeToString(tx.Body.Hash[:]),
			}).Errorln("Encode transaction failed.")
			return err
		}

		values[idx+1] = txWriter.Bytes()
	}

	return db.BatchInsert(keys, values)
}

// databaseWriter 负责插入数据到数据库的协程
func (bc *blockChain) databaseWriter() {
	select {
	case block := <-bc.dbWriterQueue:
		err := bc.InsertBlock(block)

		if err != nil {
			log.WithField("error", err).Errorln("Insert block to database failed")
		}
	}
}
