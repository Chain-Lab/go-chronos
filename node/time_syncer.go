// Package node
// @Description: 时间同步器
package node

import (
	"github.com/chain-lab/go-norn/metrics"
	"github.com/chain-lab/go-norn/p2p"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"sync"
	"time"
)

type SyncStatus int8

const (
	//syncInterval       = 5 * time.Second  // 时间同步间隔
	syncInterval = 3 * time.Second // 时间同步间隔
	//autoSyncInterval   = 10 * time.Second // 自动重启任务间隔
	confirmThreshold   = 5    // 时间同步确认阈值
	availableThreshold = 1000 // 1000 ms 容忍范围
)

const (
	INITIAL    SyncStatus = 1
	SYNCED     SyncStatus = 2
	CONFIRMING SyncStatus = 3
)

type TimeSyncer struct {
	syncerLock sync.RWMutex
	timer      *time.Ticker
	genesis    bool

	// 需要 syncerLock 加锁才能进行修改、读取
	status       SyncStatus
	delta        int64
	confirmTimes int
}

func NewTimeSyncer(genesis bool, delta int64) *TimeSyncer {
	metrics.TimeSyncerStatusSet(int8(INITIAL))
	return &TimeSyncer{
		status:       INITIAL,
		delta:        delta,
		confirmTimes: 0,
		genesis:      genesis,
	}
}

// syncRoutine 时间同步协程函数，每隔 syncInterval 选择节点发出一次同步请求
func (ts *TimeSyncer) syncRoutine() {
	log.Infoln("Start time syncer routine.")
	ts.timer = time.NewTicker(syncInterval)
	for {
		select {
		case <-ts.timer.C:
			pm := GetP2PManager()
			peersLen := len(pm.peerSet)

			if peersLen <= 0 {
				continue
			}

			r := rand.New(rand.NewSource(time.Now().UnixMilli()))
			peer := pm.peerSet[r.Intn(peersLen)]

			msg := &p2p.TimeSyncMsg{
				Code:       0,
				ReqTime:    ts.GetLogicClock(),
				RecReqTime: 0,
				RspTime:    0,
				RecRspTime: 0,
			}
			requestTimeSync(msg, peer)
			log.Infoln("Request to remote time sync.")
		}
	}
}

// todo: 添加上下文
func (ts *TimeSyncer) Start() {
	if !ts.genesis {
		metrics.RoutineCreateCounterObserve(27)
		go ts.syncRoutine()
	} else {
		metrics.TimeSyncerStatusSet(int8(SYNCED))
		ts.status = SYNCED
	}
}

// GetLogicClock 计算逻辑时钟 = 物理时钟 + 网络误差
func (ts *TimeSyncer) GetLogicClock() int64 {
	// 开启读锁，并且 defer 解锁
	ts.syncerLock.RLock()
	defer ts.syncerLock.RUnlock()

	return time.Now().UnixMilli() + ts.delta
}

// ProcessSyncRequest 处理时间同步请求消息
func (ts *TimeSyncer) ProcessSyncRequest(msg *p2p.TimeSyncMsg, p *Peer) {
	//if ts.status == SYNCED {
	msg.RspTime = ts.GetLogicClock()
	//} else {
	//	msg.Code = -1
	//}

	//metrics.RoutineCreateCounterObserve(28)
	respondTimeSync(msg, p)
}

func (ts *TimeSyncer) ProcessSyncRespond(msg *p2p.TimeSyncMsg, p *Peer) {
	if msg.Code != 0 {
		log.Warningln("Remote peer respond time sync error.")
		return
	}

	// 计算本地节点和请求同步对节点之间的时间差
	delta := (msg.RspTime-msg.RecRspTime)/2 + (msg.RecReqTime-msg.ReqTime)/2
	log.Infof("Time sync delta = %d.", delta) //todo: summary

	ts.syncerLock.Lock()
	defer ts.syncerLock.Unlock()
	if ts.status == INITIAL {
		metrics.TimeSyncerStatusSet(int8(CONFIRMING))
		ts.status = CONFIRMING
		ts.delta += delta
		return
	}

	// 如果在容忍范围内，则调整本地偏差值
	if abs(delta) < availableThreshold {
		ts.delta += delta
		metrics.TimeSyncDeltaSet(float64(delta))
		if ts.status == CONFIRMING {
			ts.confirmTimes += 1
		}
		log.Debugf("Time syncer confirm times = %d.", ts.confirmTimes)
	} else {
		ts.confirmTimes = 0
	}

	// 如果连续确认 confirmThreshold 次后在容忍范围，则认为时间的同步完成
	if ts.status == CONFIRMING && ts.confirmTimes == confirmThreshold {
		metrics.TimeSyncerStatusSet(int8(SYNCED))
		ts.status = SYNCED
		log.Infoln("Time syncer sync finished.")
	}
}

func (ts *TimeSyncer) synced() bool {
	// 开启读锁，并且 defer 解锁
	ts.syncerLock.RLock()
	defer ts.syncerLock.RUnlock()

	return ts.status == SYNCED
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
