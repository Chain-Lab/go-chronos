/**
  @author: decision
  @date: 2023/3/15
  @note: 一系列的消息处理函数
**/

package node

import (
	"encoding/binary"
	"encoding/hex"
	log "github.com/sirupsen/logrus"
	"go-chronos/common"
	"go-chronos/crypto"
	"go-chronos/p2p"
	"go-chronos/utils"
	"math/big"
)

func handleStatusMsg(h *Handler, msg *p2p.Message, p *Peer) {
	payload := msg.Payload
	height := int64(binary.LittleEndian.Uint64(payload))

	log.Debugf("Remote height = %d.", height)
	//if height > h.chain.Height() {
	//	log.WithField("height", h.chain.Height()+1).Debugln("Request block.")
	//	requestBlockWithHeight(h.chain.Height()+1, p)
	//}
}

// handleNewBlockMsg 接收对端节点的新区块
func handleNewBlockMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	if status == blockSyncing || status == syncPaused {
		return
	}

	payload := msg.Payload
	block, err := utils.DeserializeBlock(payload)

	if err != nil {
		log.WithField("error", err).Debugln("Deserialize block from bytes failed.")
		return
	}

	blockHash := block.Header.BlockHash
	strHash := hex.EncodeToString(blockHash[:])
	if h.knownBlock.Contains(strHash) {
		return
	}

	h.markBlock(strHash)
	p.MarkBlock(strHash)

	if block.Header.Height == 0 {
		go h.chain.InsertBlock(block)
		return
	}

	if verifyBlockVRF(block) {
		h.chain.AppendBlockTask(block)
		h.blockBroadcastQueue <- block
	} else {
		log.Warning("Block VRF verify failed.")
	}
}

func handleNewBlockHashMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	if status == blockSyncing || status == syncPaused {
		return
	}

	payload := msg.Payload
	blockHash := [32]byte(payload)

	if h.knownBlock.Contains(blockHash) {
		return
	}

	go requestBlockWithHash(blockHash, p)
}

func handleBlockMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	log.WithField("status", status).Traceln("Receive block.")
	if status != synced {
		return
	}

	payload := msg.Payload
	block, err := utils.DeserializeBlock(payload)

	if err != nil {
		log.WithField("error", err).Debugln("Deserialize block from bytes failed.")
		return
	}

	blockHash := block.Header.BlockHash
	strHash := hex.EncodeToString(blockHash[:])
	h.markBlock(strHash)
	p.MarkBlock(strHash)

	log.WithField("height", block.Header.Height).Infoln("Receive block.")

	if block.Header.Height == 0 {
		go h.chain.InsertBlock(block)
		return
	}

	if verifyBlockVRF(block) {
		h.chain.AppendBlockTask(block)
	}
}

func handleTransactionMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	if status != synced {
		return
	}

	payload := msg.Payload
	transaction, err := utils.DeserializeTransaction(payload)

	if err != nil {
		log.WithField("error", err).Debugln("Deserializer transaction failed.")
		return
	}

	txHash := hex.EncodeToString(transaction.Body.Hash[:])

	if h.isKnownTransaction(transaction.Body.Hash) {
		return
	}

	p.MarkTransaction(txHash)
	h.markTransaction(txHash)
	h.txPool.Add(transaction)
	h.txBroadcastQueue <- transaction
}

func handleNewPooledTransactionHashesMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	if status != synced {
		return
	}

	txHash := common.Hash(msg.Payload)
	if h.isKnownTransaction(txHash) {
		return
	}

	go requestTransactionWithHash(txHash, p)
}

func handleGetPooledTransactionMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	if status != synced {
		return
	}

	txHash := common.Hash(msg.Payload)
	strHash := hex.EncodeToString(txHash[:])

	tx := h.txPool.Get(strHash)

	go respondGetPooledTransaction(tx, p)
}

func handleSyncStatusReq(h *Handler, msg *p2p.Message, p *Peer) {
	message := h.StatusMessage()
	go respondGetSyncStatus(message, p)
}

func handleSyncStatusMsg(h *Handler, msg *p2p.Message, p *Peer) {
	payload := msg.Payload

	statusMessage, _ := utils.DeserializeStatusMsg(payload)
	h.blockSyncer.appendStatusMsg(statusMessage)
}

// handleSyncGetBlocksMsg 处理获取某个高度的区块
func handleSyncGetBlocksMsg(h *Handler, msg *p2p.Message, p *Peer) {
	status := h.blockSyncer.getStatus()
	if status != synced {
		return
	}

	// 从消息中直接转换得到需要的区块高度
	payload := msg.Payload
	height := int64(binary.LittleEndian.Uint64(payload))

	// 从链上获取到区块
	block, err := h.chain.GetBlockByHeight(height)
	if err != nil {
		log.WithField("error", err).Debugln("Get block with height failed.")
		return
	}

	go respondSyncGetBlock(block, p)
}

func handleSyncBlockMsg(h *Handler, msg *p2p.Message, p *Peer) {
	payload := msg.Payload
	block, err := utils.DeserializeBlock(payload)

	if err != nil {
		log.WithField("error", err).Debugln("Block deserialize failed.")
		return
	}
	h.appendBlockToSyncer(block)
}

func verifyBlockVRF(block *common.Block) bool {
	//println(hex.EncodeToString(block.Header.PublicKey[:]))
	bytesParams := block.Header.Params
	params, err := utils.DeserializeGeneralParams(bytesParams)

	// todo: 如果这里的数据不全，导致反序列化出错可能会使得这个区块无法正常添加
	if err != nil {
		log.WithField("error", err).Warning("Deserialize params failed.")
		return false
	}

	s := new(big.Int)
	t := new(big.Int)
	publicKey := crypto.Bytes2PublicKey(block.Header.PublicKey[:])

	s.SetBytes(params.S)
	t.SetBytes(params.T)

	verified, err := crypto.VRFCheckRemoteConsensus(publicKey, params.Result, s, t, params.RandomNumber[:])

	if err != nil || !verified {
		log.Debugln("Verify VRF failed.")
		return false
	}

	return true
}