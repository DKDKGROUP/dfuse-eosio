package tokenhist

import (
	"errors"
	"fmt"

	"github.com/dfuse-io/bstream"
	"github.com/dfuse-io/dfuse-eosio/accounthist"
	"github.com/dfuse-io/dfuse-eosio/accounthist/grpc"
	"github.com/dfuse-io/dfuse-eosio/accounthist/injector"
	"github.com/dfuse-io/dfuse-eosio/accounthist/keyer"
	"github.com/dfuse-io/dstore"
	"github.com/dfuse-io/kvdb/store"
	"github.com/dfuse-io/shutter"
	"go.uber.org/zap"
)

type startFunc func()
type stopFunc func(error)

type Config struct {
	KvdbDSN                  string
	GRPCListenAddr           string
	BlocksStoreURL           string //FileSourceBaseURL
	BlockstreamAddr          string // LiveSourceAddress
	ShardNum                 byte
	MaxEntriesPerKey         uint64
	FlushBlocksInterval      uint64
	EnableInjector           bool
	EnableServer             bool
	IgnoreCheckpointOnLaunch bool
	StartBlockNum            uint64
	StopBlockNum             uint64
	AccounthistMode          accounthist.AccounthistMode
}

type Modules struct {
	BlockFilter func(blk *bstream.Block) error
	Tracker     *bstream.Tracker
}

type App struct {
	*shutter.Shutter
	config  *Config
	modules *Modules
}

func New(config *Config, modules *Modules) *App {
	app := &App{
		Shutter: shutter.New(),
		config:  config,
		modules: modules,
	}

	return app
}

func (a *App) Run() error {
	zlog.Info("starting accounthist app",
		zap.Reflect("config", a.config),
	)

	if err := a.config.validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	kvdb, err := store.New(a.config.KvdbDSN)
	if err != nil {
		zlog.Fatal("could not create kvstore", zap.Error(err))
	}

	if true {
		kvdb = injector.NewRWCache(kvdb)
	}

	blocksStore, err := dstore.NewDBinStore(a.config.BlocksStoreURL)
	if err != nil {
		return fmt.Errorf("setting up archive store: %w", err)
	}

	switch a.config.AccounthistMode {
	case accounthist.AccounthistModeAccount:
		setupAccountMode()
	case accounthist.AccounthistModeAccountContract:
		setupAccountContractMode()
	default:
		return fmt.Errorf("invalid accounthist mode: %q", a.config.AccounthistMode)
	}

	if a.config.EnableServer {
		server := grpc.New(a.config.GRPCListenAddr, a.config.MaxEntriesPerKey, kvdb)

		a.OnTerminating(server.Terminate)
		server.OnTerminated(a.Shutdown)

		switch a.config.AccounthistMode {
		case accounthist.AccounthistModeAccount:
			go server.ServeAccountMode()
		case accounthist.AccounthistModeAccountContract:
			go server.ServeAccountContractMode()
		default:
			return fmt.Errorf("invalid accounthist mode: %q", a.config.AccounthistMode)
		}
	}

	if a.config.EnableInjector {
		injector := injector.NewInjector(
			kvdb,
			blocksStore,
			a.modules.BlockFilter,
			a.config.ShardNum,
			a.config.MaxEntriesPerKey,
			a.config.FlushBlocksInterval,
			a.config.StartBlockNum,
			a.config.StopBlockNum,
			a.modules.Tracker,
		)

		if err = injector.SetupSource(a.config.IgnoreCheckpointOnLaunch); err != nil {
			return fmt.Errorf("error setting up source: %w", err)
		}

		a.OnTerminating(injector.Shutdown)
		injector.OnTerminated(a.Shutdown)

		go injector.Launch()
	}

	return nil
}

func (c *Config) validate() error {
	if !c.EnableInjector && !c.EnableServer {
		return errors.New("both enable injection and enable server were disabled, this is invalid, at least one of them must be enabled, or both")
	}

	return nil
}

func setupAccountContractMode() {
	zlog.Info("setting up 'account-contract' mode")
	injector.ActionKeyGenerator = accounthist.NewAccountContractKey
	injector.CheckpointKeyGenerator = keyer.EncodeAccountContractCheckpointKey
	injector.InjectorRowKeyDecoder = accounthist.AccountContractKeyRowDecoder
}

func setupAccountMode() {
	zlog.Info("setting up 'account' mode")
	injector.ActionKeyGenerator = accounthist.NewAccountKey
	injector.CheckpointKeyGenerator = keyer.EncodeAccountCheckpointKey
	injector.InjectorRowKeyDecoder = accounthist.AccountKeyRowDecoder
}
