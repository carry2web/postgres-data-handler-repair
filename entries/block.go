package entries

import (
	"context"
	"encoding/hex"
	"reflect"
	"time"

	"github.com/deso-protocol/core/lib"
	"github.com/deso-protocol/state-consumer/consumer"
	"github.com/pkg/errors"
	"github.com/uptrace/bun"
)

type BlockEntry struct {
	BlockHash                    string `pg:",pk,use_zero"`
	PrevBlockHash                string
	TxnMerkleRoot                string
	Timestamp                    time.Time
	Height                       uint64
	Nonce                        uint64
	ExtraNonce                   uint64
	BlockVersion                 uint32
	ProposerVotingPublicKey      string `pg:",use_zero"`
	ProposerRandomSeedSignature  string `pg:",use_zero"`
	ProposedInView               uint64
	ProposerVotePartialSignature string `pg:",use_zero"`
	// TODO: Quorum Certificates. Separate entry.

	BadgerKey []byte `pg:",use_zero"`
}

type PGBlockEntry struct {
	bun.BaseModel `bun:"table:block"`
	BlockEntry
}

type BlockSigner struct {
	BlockHash   string
	SignerIndex uint64
}

type PGBlockSigner struct {
	bun.BaseModel `bun:"table:block_signer"`
	BlockSigner
}

// Convert the UserAssociation DeSo encoder to the PG struct used by bun.
func BlockEncoderToPGStruct(block *lib.MsgDeSoBlock, keyBytes []byte, params *lib.DeSoParams) (*PGBlockEntry, []*PGBlockSigner) {
	// Use keyBytes if provided (from state-consumer or API), otherwise compute hash
	var blockHashHex string
	if len(keyBytes) > 0 {
		blockHashHex = hex.EncodeToString(keyBytes)
	} else {
		blockHash, _ := block.Hash()
		if blockHash != nil {
			blockHashHex = hex.EncodeToString(blockHash[:])
		}
	}
	qc := block.Header.GetQC()
	blockSigners := []*PGBlockSigner{}
	if !isInterfaceNil(qc) {
		aggSig := qc.GetAggregatedSignature()
		if !isInterfaceNil(aggSig) {
			signersList := aggSig.GetSignersList()
			if !isInterfaceNil(signersList) {
				for ii := 0; ii < signersList.Size(); ii++ {
					// Skip signers that didn't sign.
					if !signersList.Get(ii) {
						continue
					}
					blockSigners = append(blockSigners, &PGBlockSigner{
						BlockSigner: BlockSigner{
							BlockHash:   blockHashHex,
							SignerIndex: uint64(ii),
						},
					})
				}
			}
		}
	}

	// Handle PoS fields (nil for old PoW blocks)
	var proposerVotingPublicKey string
	var proposerRandomSeedSignature string
	var proposerVotePartialSignature string

	if block.Header.ProposerVotingPublicKey != nil {
		proposerVotingPublicKey = block.Header.ProposerVotingPublicKey.ToString()
	}
	if block.Header.ProposerRandomSeedSignature != nil {
		proposerRandomSeedSignature = block.Header.ProposerRandomSeedSignature.ToString()
	}
	if block.Header.ProposerVotePartialSignature != nil {
		proposerVotePartialSignature = block.Header.ProposerVotePartialSignature.ToString()
	}

	// Handle potentially nil hash fields for old blocks
	var prevBlockHash string
	var txnMerkleRoot string

	if block.Header.PrevBlockHash != nil {
		prevBlockHash = hex.EncodeToString(block.Header.PrevBlockHash[:])
	}
	if block.Header.TransactionMerkleRoot != nil {
		txnMerkleRoot = hex.EncodeToString(block.Header.TransactionMerkleRoot[:])
	}

	return &PGBlockEntry{
		BlockEntry: BlockEntry{
			BlockHash:                    blockHashHex,
			PrevBlockHash:                prevBlockHash,
			TxnMerkleRoot:                txnMerkleRoot,
			Timestamp:                    consumer.UnixNanoToTime(uint64(block.Header.TstampNanoSecs)),
			Height:                       block.Header.Height,
			Nonce:                        block.Header.Nonce,
			ExtraNonce:                   block.Header.ExtraNonce,
			BlockVersion:                 block.Header.Version,
			ProposerVotingPublicKey:      proposerVotingPublicKey,
			ProposerRandomSeedSignature:  proposerRandomSeedSignature,
			ProposedInView:               block.Header.ProposedInView,
			ProposerVotePartialSignature: proposerVotePartialSignature,
			BadgerKey:                    keyBytes,
		},
	}, blockSigners
}

// PostBatchOperation is the entry point for processing a batch of post entries. It determines the appropriate handler
// based on the operation type and executes it.
func BlockBatchOperation(entries []*lib.StateChangeEntry, db bun.IDB, params *lib.DeSoParams) error {
	// We check before we call this function that there is at least one operation type.
	// We also ensure before this that all entries have the same operation type.
	operationType := entries[0].OperationType
	var err error
	if operationType == lib.DbOperationTypeDelete {
		err = bulkDeleteBlockEntry(entries, db, operationType)
	} else {
		err = bulkInsertBlockEntry(entries, db, operationType, params)
	}
	if err != nil {
		return errors.Wrapf(err, "entries.PostBatchOperation: Problem with operation type %v", operationType)
	}
	return nil
}

// bulkInsertUtxoOperationsEntry inserts a batch of user_association entries into the database.
func bulkInsertBlockEntry(entries []*lib.StateChangeEntry, db bun.IDB, operationType lib.StateSyncerOperationType, params *lib.DeSoParams) error {
	// If this block is a part of the initial sync, skip it - it will be handled by the utxo operations.
	if operationType == lib.DbOperationTypeInsert {
		return nil
	}

	// Track the unique entries we've inserted so we don't insert the same entry twice.
	uniqueBlocks := consumer.UniqueEntries(entries)

	// Create a new array to hold the bun struct.
	pgBlockEntrySlice := make([]*PGBlockEntry, 0)
	pgTransactionEntrySlice := make([]*PGTransactionEntry, 0)
	pgBlockSignersEntrySlice := make([]*PGBlockSigner, 0)

	for _, entry := range uniqueBlocks {
		block := entry.Encoder.(*lib.MsgDeSoBlock)
		blockEntry, blockSigners := BlockEncoderToPGStruct(block, entry.KeyBytes, params)
		pgBlockEntrySlice = append(pgBlockEntrySlice, blockEntry)
		pgBlockSignersEntrySlice = append(pgBlockSignersEntrySlice, blockSigners...)
		for jj, transaction := range block.Txns {
			indexInBlock := uint64(jj)
			pgTransactionEntry, err := TransactionEncoderToPGStruct(
				transaction,
				&indexInBlock,
				blockEntry.BlockHash,
				blockEntry.Height,
				blockEntry.Timestamp,
				nil,
				nil,
				params,
			)
			if err != nil {
				return errors.Wrapf(err, "entries.bulkInsertBlockEntry: Problem converting transaction to PG struct")
			}
			pgTransactionEntrySlice = append(pgTransactionEntrySlice, pgTransactionEntry)
			if transaction.TxnMeta.GetTxnType() != lib.TxnTypeAtomicTxnsWrapper {
				continue
			}
			innerTxns, err := parseInnerTxnsFromAtomicTxn(pgTransactionEntry, params)
			if err != nil {
				return errors.Wrapf(err, "entries.bulkInsertBlockEntry: Problem parsing inner txns from atomic txn")
			}
			pgTransactionEntrySlice = append(pgTransactionEntrySlice, innerTxns...)
		}
	}

	blockQuery := db.NewInsert().Model(&pgBlockEntrySlice)

	if operationType == lib.DbOperationTypeUpsert {
		// Handle conflicts on block_hash primary key
		blockQuery = blockQuery.On("CONFLICT (block_hash) DO UPDATE")
	}

	result, err := blockQuery.Exec(context.Background())
	if err != nil {
		return errors.Wrapf(err, "entries.bulkInsertBlock: Error inserting entries")
	}

	// Verify that PostgreSQL confirmed the insert/update
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrapf(err, "entries.bulkInsertBlock: Error getting rows affected")
	}
	if rowsAffected != int64(len(pgBlockEntrySlice)) {
		return errors.Errorf("entries.bulkInsertBlock: Expected %d rows affected, got %d", len(pgBlockEntrySlice), rowsAffected)
	}

	if err := bulkInsertTransactionEntry(pgTransactionEntrySlice, db, operationType); err != nil {
		return errors.Wrapf(err, "entries.bulkInsertBlock: Error inserting transaction entries")
	}

	if len(pgBlockSignersEntrySlice) > 0 {
		// Execute the insert query.
		query := db.NewInsert().Model(&pgBlockSignersEntrySlice)

		if operationType == lib.DbOperationTypeUpsert {
			query = query.On("CONFLICT (block_hash, signer_index) DO UPDATE")
		}

		if _, err := query.Returning("").Exec(context.Background()); err != nil {
			return errors.Wrapf(err, "entries.bulkInsertBlockEntry: Error inserting block signers")
		}
	}

	return nil
}

// bulkDeleteBlockEntry deletes a batch of block entries from the database.
func bulkDeleteBlockEntry(entries []*lib.StateChangeEntry, db bun.IDB, operationType lib.StateSyncerOperationType) error {
	// Track the unique entries we've inserted so we don't insert the same entry twice.
	uniqueEntries := consumer.UniqueEntries(entries)

	// Transform the entries into a list of keys to delete.
	keysToDelete := consumer.KeysToDelete(uniqueEntries)

	return bulkDeleteBlockEntriesFromKeysToDelete(db, keysToDelete)
}

// bulkDeleteBlockEntriesFromKeysToDelete deletes a batch of block entries from the database.
// It also deletes any transactions and utxo operations associated with the block.
func bulkDeleteBlockEntriesFromKeysToDelete(db bun.IDB, keysToDelete [][]byte) error {
	// Execute the delete query on the blocks table.
	if _, err := db.NewDelete().
		Model(&PGBlockEntry{}).
		Where("badger_key IN (?)", bun.In(keysToDelete)).
		Returning("").
		Exec(context.Background()); err != nil {
		return errors.Wrapf(err, "entries.bulkDeleteBlockEntry: Error deleting entries")
	}
	// Get block hashes from keys to delete.
	blockHashHexesToDelete := make([]string, len(keysToDelete))
	for ii, keyToDelete := range keysToDelete {
		blockHashHexesToDelete[ii] = hex.EncodeToString(consumer.GetBlockHashBytesFromKey(keyToDelete))
	}

	// Delete any transactions associated with the block.
	if _, err := db.NewDelete().
		Model(&PGTransactionEntry{}).
		Where("block_hash IN (?)", bun.In(blockHashHexesToDelete)).
		Returning("").
		Exec(context.Background()); err != nil {
		return errors.Wrapf(err, "entries.bulkDeleteBlockEntry: Error deleting transaction entries")
	}

	// Delete any utxo operations associated with the block.
	if _, err := db.NewDelete().
		Model(&PGUtxoOperationEntry{}).
		Where("block_hash IN (?)", bun.In(blockHashHexesToDelete)).
		Returning("").
		Exec(context.Background()); err != nil {
		return errors.Wrapf(err, "entries.bulkDeleteBlockEntry: Error deleting utxo operation entries")
	}

	// Delete any signers associated with the block.
	if _, err := db.NewDelete().
		Model(&PGBlockSigner{}).
		Where("block_hash IN (?)", bun.In(blockHashHexesToDelete)).
		Returning("").
		Exec(context.Background()); err != nil {
		return errors.Wrapf(err, "entries.bulkDeleteBlockEntry: Error deleting block signers")
	}

	// Delete any stake rewards associated with the block.
	if _, err := db.NewDelete().
		Model(&PGStakeReward{}).
		Where("block_hash IN (?)", bun.In(blockHashHexesToDelete)).
		Returning("").
		Exec(context.Background()); err != nil {
		return errors.Wrapf(err, "entries.bulkDeleteBlockEntry: Error deleting stake rewards")
	}
	return nil
}

// golang interface types are stored as a tuple of (type, value). A single i==nil check is not enough to
// determine if a pointer that implements an interface is nil. This function checks if the interface is nil
// by checking if the pointer itself is nil.
func isInterfaceNil(i interface{}) bool {
	if i == nil {
		return true
	}

	value := reflect.ValueOf(i)
	return value.Kind() == reflect.Ptr && value.IsNil()
}

func BlockNodeOperation(entries []*lib.StateChangeEntry, db bun.IDB, params *lib.DeSoParams) error {
	operationType := entries[0].OperationType
	if operationType == lib.DbOperationTypeDelete {
		// This should NEVER happen.
		return errors.New("BlockNodeOperation: Delete operation not supported")
	}

	uniqueBlockNodes := consumer.UniqueEntries(entries)
	blockHashesToDelete := []*lib.BlockHash{}
	for _, entry := range uniqueBlockNodes {
		blockNode := entry.Encoder.(*lib.BlockNode)
		if !blockNode.IsCommitted() {
			blockHashesToDelete = append(blockHashesToDelete, blockNode.Hash)
		}
	}

	if len(blockHashesToDelete) == 0 {
		return nil
	}

	blockKeysToDelete := make([][]byte, len(blockHashesToDelete))
	for ii, blockHashToDelete := range blockHashesToDelete {
		blockKeysToDelete[ii] = lib.BlockHashToBlockKey(blockHashToDelete)
	}

	return bulkDeleteBlockEntriesFromKeysToDelete(db, blockKeysToDelete)
}
