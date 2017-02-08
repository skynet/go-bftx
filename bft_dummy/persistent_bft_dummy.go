package bft_dummy

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"strings"
	"fmt"	//JCNM

	. "github.com/tendermint/go-common"
	dbm "github.com/tendermint/go-db"
	"github.com/tendermint/go-merkle"
	"github.com/tendermint/go-wire"
	"github.com/tendermint/abci/types"
)

const (
	ValidatorSetChangePrefix string = "val:"
)

//-----------------------------------------

type PersistentBftApplication struct {
	app *BftApplication
	db  dbm.DB

	// latest received
	// TODO: move to merkle tree?
	blockHeader *types.Header

	// validator set
	changes []*types.Validator
}

func NewPersistentBftApplication(dbDir string) *PersistentBftApplication {
	db := dbm.NewDB("dummy", "leveldb", dbDir)
	lastBlock := LoadLastBlock(db)

	stateTree := merkle.NewIAVLTree(0, db)
	stateTree.Load(lastBlock.AppHash)

	//log.Notice("Loaded state", "block", lastBlock.Height, "root", stateTree.Hash())	JCNM

	return &PersistentBftApplication{
		app: &BftApplication{state: stateTree},
		db:  db,
	}
}

func (app *PersistentBftApplication) Info() (resInfo types.ResponseInfo) {
	resInfo = app.app.Info()
	lastBlock := LoadLastBlock(app.db)
	resInfo.LastBlockHeight = lastBlock.Height
	resInfo.LastBlockAppHash = lastBlock.AppHash
	return resInfo
}

func (app *PersistentBftApplication) SetOption(key string, value string) (log string) {
	return app.app.SetOption(key, value)
}

// tx is either "key=value" or just arbitrary bytes
func (app *PersistentBftApplication) DeliverTx(tx []byte) types.Result {
	// if it starts with "val:", update the validator set
	// format is "val:pubkey/power"
	if isValidatorTx(tx) {
		// update validators in the merkle tree
		// and in app.changes
		return app.execValidatorTx(tx)
	}

	// otherwise, update the key-value store
	return app.app.DeliverTx(tx)
}

func (app *PersistentBftApplication) CheckTx(tx []byte) types.Result {
	return app.app.CheckTx(tx)
}

func (app *PersistentBftApplication) Commit() types.Result {
	// Save
	appHash := app.app.state.Save()
	//log.Info("Saved state", "root", appHash)	JCNM

	lastBlock := LastBlockInfo{
		Height:  app.blockHeader.Height,
		AppHash: appHash, // this hash will be in the next block header
	}
	SaveLastBlock(app.db, lastBlock)
	return types.NewResultOK(appHash, "")
}

func (app *PersistentBftApplication) Query(query []byte) types.Result {
	return app.app.Query(query)
}

// Save the validators in the merkle tree
func (app *PersistentBftApplication) InitChain(validators []*types.Validator) {
	for _, v := range validators {
		r := app.updateValidator(v)
		if r.IsErr() {
			//log.Error("Error updating validators", "r", r) JCNM
			fmt.Println("Error updating validators")	//JCNM
		}
	}
}

// Track the block hash and header information
func (app *PersistentBftApplication) BeginBlock(hash []byte, header *types.Header) {
	// update latest block info
	app.blockHeader = header

	// reset valset changes
	app.changes = make([]*types.Validator, 0)
}

// Update the validator set
func (app *PersistentBftApplication) EndBlock(height uint64) (resEndBlock types.ResponseEndBlock) {
	return types.ResponseEndBlock{Diffs: app.changes}
}

//-----------------------------------------
// persist the last block info

var lastBlockKey = []byte("lastblock")

type LastBlockInfo struct {
	Height  uint64
	AppHash []byte
}

// Get the last block from the db
func LoadLastBlock(db dbm.DB) (lastBlock LastBlockInfo) {
	buf := db.Get(lastBlockKey)
	if len(buf) != 0 {
		r, n, err := bytes.NewReader(buf), new(int), new(error)
		wire.ReadBinaryPtr(&lastBlock, r, 0, n, err)
		if *err != nil {
			// DATA HAS BEEN CORRUPTED OR THE SPEC HAS CHANGED
			Exit(Fmt("Data has been corrupted or its spec has changed: %v\n", *err))
		}
		// TODO: ensure that buf is completely read.
	}

	return lastBlock
}

func SaveLastBlock(db dbm.DB, lastBlock LastBlockInfo) {
	//log.Notice("Saving block", "height", lastBlock.Height, "root", lastBlock.AppHash)	//JCNM
	buf, n, err := new(bytes.Buffer), new(int), new(error)
	wire.WriteBinary(lastBlock, buf, n, err)
	if *err != nil {
		// TODO
		PanicCrisis(*err)
	}
	db.Set(lastBlockKey, buf.Bytes())
}

//---------------------------------------------
// update validators

func (app *PersistentBftApplication) Validators() (validators []*types.Validator) {
	app.app.state.Iterate(func(key, value []byte) bool {
		if isValidatorTx(key) {
			validator := new(types.Validator)
			err := types.ReadMessage(bytes.NewBuffer(value), validator)
			if err != nil {
				panic(err)
			}
			validators = append(validators, validator)
		}
		return false
	})
	return
}

func MakeValSetChangeTx(pubkey []byte, power uint64) []byte {
	return []byte(Fmt("val:%X/%d", pubkey, power))
}

func isValidatorTx(tx []byte) bool {
	if strings.HasPrefix(string(tx), ValidatorSetChangePrefix) {
		return true
	}
	return false
}

// format is "val:pubkey1/power1,addr2/power2,addr3/power3"tx
func (app *PersistentBftApplication) execValidatorTx(tx []byte) types.Result {
	tx = tx[len(ValidatorSetChangePrefix):]
	pubKeyAndPower := strings.Split(string(tx), "/")
	if len(pubKeyAndPower) != 2 {
		return types.ErrEncodingError.SetLog(Fmt("Expected 'pubkey/power'. Got %v", pubKeyAndPower))
	}
	pubkeyS, powerS := pubKeyAndPower[0], pubKeyAndPower[1]
	pubkey, err := hex.DecodeString(pubkeyS)
	if err != nil {
		return types.ErrEncodingError.SetLog(Fmt("Pubkey (%s) is invalid hex", pubkeyS))
	}
	power, err := strconv.Atoi(powerS)
	if err != nil {
		return types.ErrEncodingError.SetLog(Fmt("Power (%s) is not an int", powerS))
	}

	// update
	return app.updateValidator(&types.Validator{pubkey, uint64(power)})
}

// add, update, or remove a validator
func (app *PersistentBftApplication) updateValidator(v *types.Validator) types.Result {
	key := []byte("val:" + string(v.PubKey))
	if v.Power == 0 {
		// remove validator
		if !app.app.state.Has(key) {
			return types.ErrUnauthorized.SetLog(Fmt("Cannot remove non-existent validator %X", key))
		}
		app.app.state.Remove(key)
	} else {
		// add or update validator
		value := bytes.NewBuffer(make([]byte, 0))
		if err := types.WriteMessage(v, value); err != nil {
			return types.ErrInternalError.SetLog(Fmt("Error encoding validator: %v", err))
		}
		app.app.state.Set(key, value.Bytes())
	}

	// we only update the changes array if we succesfully updated the tree
	app.changes = append(app.changes, v)

	return types.OK
}
