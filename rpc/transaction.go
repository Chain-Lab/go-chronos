/**
  @author: decision
  @date: 2023/6/16
  @note:
**/

package rpc

import (
	"context"
	"encoding/hex"
	"github.com/gogo/protobuf/proto"
	"go-chronos/core"
	"go-chronos/rpc/pb"
	"go-chronos/utils"
)

type transactionService struct {
	pb.UnimplementedTransactionServer
}

//var TransactionService = transactionService{}

func (s *transactionService) SubmitTransaction(ctx context.Context, in *pb.SubmitTransactionReq) (*pb.SubmitTransactionRsp, error) {
	resp := new(pb.SubmitTransactionRsp)
	recvTransactionCode := in.GetSignedTransaction()

	bytesTransaction, err := hex.DecodeString(recvTransactionCode)

	if err != nil {
		resp.Status = pb.SubmitTransactionStatus_DECODE_FAILED.Enum()
		resp.Error = proto.String("Decode transaction to bytes failed.")
		//log.Infoln("Decode transaction to bytes failed.")
		return resp, err
	}

	transaction, err := utils.DeserializeTransaction(bytesTransaction)

	if err != nil {
		resp.Status = pb.SubmitTransactionStatus_DESERIALIZE_FAILED.Enum()
		resp.Error = proto.String("Deserialize transaction failed.")
		//log.Infoln("Deserialize transaction failed.")
		return resp, err
	}

	if !transaction.Verify() {
		resp.Status = pb.SubmitTransactionStatus_SIGNATURE_FAILED.Enum()
		resp.Error = proto.String("Verify transaction signature failed.")
		//log.Infoln("Verify transaction signature failed.")
		return resp, err
	}

	pool := core.GetTxPoolInst()
	pool.Add(transaction)

	//log.Infoln("Append transaction successful.")
	resp.Status = pb.SubmitTransactionStatus_SUCCESS.Enum()
	resp.Error = proto.String("Success.")
	return resp, err
}