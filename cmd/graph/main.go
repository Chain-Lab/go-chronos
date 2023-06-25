package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	log "github.com/sirupsen/logrus"
	"go-chronos/core"
	"go-chronos/utils"
	"os"
)

var (
	datadir string
	height  int
	help    bool
)

func init() {
	flag.IntVar(&height, "height", 0, "Scan height")
	flag.StringVar(&datadir, "d", "./data", "Data directory path")
	flag.BoolVar(&help, "help", false, "Command help")

	flag.Usage = usage
}

func usage() {
	fmt.Fprintf(os.Stderr, `chronos version: 1.0.0
Usage: chronos [-d datadir] [-h help] [--height]

Options:
`)
	flag.PrintDefaults()
}

func main() {
	flag.Parse()
	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()

	// 数据库、节点的启动

	if help {
		flag.Usage()
		return
	}

	println(datadir)
	db, err := utils.NewLevelDB(datadir)

	if err != nil {
		log.WithField("error", err).Errorln("Create or load database failed.")
		return
	}

	chain := core.NewBlockchain(db)

	var timestamps []int64
	var txs []int

	prevBlock, err := chain.GetBlockByHeight(0)

	if err != nil {
		log.Errorln(err)
	}

	//fmt.Printf(hex.EncodeToString(prevBlock.Header.BlockHash[:]))
	total := 0

	for i := 1; i < height; i++ {
		block, err := chain.GetBlockByHeight(int64(i))
		if err != nil {
			log.WithField("height", i).Errorln("Get block failed.")
		}

		//if prevBlock.BlockHash() != block.PrevBlockHash() {
		//	log.Println("prev:", hex.EncodeToString(block.Header.PrevBlockHash[:]))
		//	log.Println("real:", prevBlock.BlockHash())
		//}

		if block.Header.Timestamp-prevBlock.Header.Timestamp < 0 {
			log.Println("prev:", hex.EncodeToString(prevBlock.Header.PublicKey[:]))
			log.Println("now:", hex.EncodeToString(block.Header.PublicKey[:]))
		}

		timestamps = append(timestamps, block.Header.Timestamp-prevBlock.Header.Timestamp)
		txs = append(txs, len(block.Transactions))
		total += len(block.Transactions)
		prevBlock = block
	}

	fmt.Printf("timestamp: %v\n", timestamps)
	fmt.Printf("counts: %v\n", txs)
	fmt.Printf("total: %d\n", total)
}