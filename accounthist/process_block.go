package accounthist

import (
	"context"
	"fmt"
	"time"

	"github.com/dfuse-io/bstream"
	"github.com/dfuse-io/bstream/forkable"
	pbcodec "github.com/dfuse-io/dfuse-eosio/pb/dfuse/eosio/codec/v1"
	"github.com/eoscanada/eos-go"
	"go.uber.org/zap"
)

func (ws *Service) ProcessBlock(blk *bstream.Block, obj interface{}) error {
	ctx := context.Background()

	block := blk.ToNative().(*pbcodec.Block)
	fObj := obj.(*forkable.ForkableObject)
	rawTraceMap := fObj.Obj.(map[uint64][]byte)
	isLastInStreak := fObj.StepIndex+1 == fObj.StepCount

	if ws.stopBlockNum != 0 && blk.Num() >= ws.stopBlockNum {
		zlog.Info("stop block num reached, flushing all writes",
			zap.Uint64("stop_block_num", ws.stopBlockNum),
			zap.Uint64("block_num", blk.Num()),
		)
		if err := ws.forceFlush(ctx); err != nil {
			ws.Shutdown(err)
			return fmt.Errorf("flushing when stopping: %w", err)
		}

		ws.Shutdown(nil)
		return nil
	}

	for _, trxTrace := range block.TransactionTraces() {
		if trxTrace.HasBeenReverted() {
			continue
		}

		actionMatcher := block.FilteringActionMatcher(trxTrace)

		for _, act := range trxTrace.ActionTraces {
			if !actionMatcher.Matched(act.ExecutionIndex) || act.Receipt == nil {
				continue
			}

			accts := map[string]bool{
				act.Receiver: true,
			}
			for _, v := range act.Action.Authorization {
				accts[v.Actor] = true
			}

			for acct := range accts {
				acctUint := eos.MustStringToName(acct)
				acctSeqData, err := ws.getSequenceData(ctx, acctUint)
				if err != nil {
					return fmt.Errorf("error while getting sequence data for account %v: %w", acct, err)
				}

				if acctSeqData.MaxEntries == 0 {
					continue
				}

				// when shard 1 starts it will based the first seen action on values in shard 0. the last aciotn for an account
				// will always have a greater last global seq
				if act.Receipt.GlobalSequence <= acctSeqData.LastGlobalSeq {
					zlog.Debug("this block has already been processed for this account",
						zap.Stringer("block", blk),
						zap.String("account", acct),
					)
					continue
				}

				lastDeletedSeq, err := ws.deleteStaleRows(ctx, acctUint, acctSeqData)
				if err != nil {
					return err
				}

				acctSeqData.LastDeletedOrdinal = lastDeletedSeq
				rawTrace := rawTraceMap[act.Receipt.GlobalSequence]

				// since the current ordinal is the last assgined order number we need to
				// increment it before we write a new action
				acctSeqData.CurrentOrdinal++
				if err = ws.writeAction(ctx, acctUint, acctSeqData, act, rawTrace); err != nil {
					return fmt.Errorf("error while writing action to store: %w", err)
				}

				acctSeqData.LastGlobalSeq = act.Receipt.GlobalSequence

				ws.updateHistorySeq(acctUint, acctSeqData)
			}
		}
	}

	if err := ws.writeLastProcessedBlock(ctx, block); err != nil {
		return fmt.Errorf("error while saving block checkpoint")
	}

	if err := ws.flush(ctx, block, isLastInStreak); err != nil {
		return fmt.Errorf("error while flushing: %w", err)
	}

	ws.currentBatchMetrics.blockCount++
	if (blk.Number % 1000) == 0 {
		zlog.Info("processed blk 1/1000",
			zap.String("block_id", block.Id),
			zap.Uint32("block_num", block.Number),
			zap.Duration("processed_blocks_duration", time.Since(ws.currentBatchMetrics.batchStartTime)),
			zap.Float64("block_rate", float64(ws.currentBatchMetrics.blockCount)/(float64(time.Since(ws.currentBatchMetrics.batchStartTime))/float64(time.Second))),
			zap.Int("cache_size", len(ws.historySeqMap)),
			zap.Uint64("cache_miss", ws.currentBatchMetrics.accountCacheMiss),
			zap.Uint64("cache_hit", ws.currentBatchMetrics.accountCacheHit),
			zap.Duration("total_read_seq_duration", ws.currentBatchMetrics.totalReadSeqDuration),
			zap.Duration("avg_read_seq_duration", ws.currentBatchMetrics.totalReadSeqDuration/time.Duration(ws.currentBatchMetrics.readSeqCallCount)),
			zap.Uint64("read_seq_call_count", ws.currentBatchMetrics.readSeqCallCount),
			zap.Duration("total_max_entry_seq_duration", ws.currentBatchMetrics.totalReadMaxEntryDuration),
			zap.Duration("avg_max_entry_duration", ws.currentBatchMetrics.totalReadMaxEntryDuration/time.Duration(ws.currentBatchMetrics.readMaxEntryCallCount)),
			zap.Uint64("read_max_entry_count", ws.currentBatchMetrics.readMaxEntryCallCount),
		)
		ws.currentBatchMetrics = blockBatchMetrics{
			batchStartTime: time.Now(),
		}
	}

	return nil
}
