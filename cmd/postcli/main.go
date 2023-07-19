package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/davecgh/go-spew/spew"
	"github.com/spacemeshos/post/config"
	"github.com/spacemeshos/post/initialization"
	"github.com/spacemeshos/post/internal/postrs"
	"github.com/spacemeshos/post/proving"
	"github.com/spacemeshos/post/shared"
	"github.com/spacemeshos/post/verifying"
	"go.uber.org/zap"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
)

const edKeyFileName = "key.bin"

var (
	cfg                = config.MainnetConfig()
	opts               = config.MainnetInitOpts()
	printProviders     bool
	printNumFiles      bool
	printConfig        bool
	genProof           bool
	idHex              string
	id                 []byte
	commitmentAtxIdHex string
	commitmentAtxId    []byte
	reset              bool
)

func parseFlags() {
	flag.BoolVar(&printProviders, "printProviders", false, "print the list of compute providers")
	flag.BoolVar(&printNumFiles, "printNumFiles", false, "print the total number of files that would be initialized")
	flag.BoolVar(&printConfig, "printConfig", false, "print the used config and options")
	flag.BoolVar(&genProof, "genproof", false, "generate proof as a sanity test, after initialization")
	flag.StringVar(&opts.DataDir, "datadir", opts.DataDir, "filesystem datadir path")
	flag.Uint64Var(&opts.MaxFileSize, "maxFileSize", opts.MaxFileSize, "max file size")
	flag.StringVar(&opts.ProviderID, "provider", opts.ProviderID, "compute provider id (required), example: 0,1,2")
	flag.Uint64Var(&cfg.LabelsPerUnit, "labelsPerUnit", cfg.LabelsPerUnit, "the number of labels per unit")
	flag.BoolVar(&reset, "reset", false, "whether to reset the datadir before starting")
	flag.StringVar(&idHex, "id", "", "miner's id (public key), in hex (will be auto-generated if not provided)")
	flag.StringVar(&commitmentAtxIdHex, "commitmentAtxId", "9eebff023abb17ccb775c602daade8ed708f0a50d3149a42801184f5b74f2865", "commitment atx id, in hex (required)")
	numUnits := flag.Uint64("numUnits", uint64(opts.NumUnits), "number of units")

	flag.IntVar(&opts.FromFileIdx, "fromFile", 0, "index of the first file to init (inclusive)")
	var to int
	flag.IntVar(&to, "toFile", math.MaxInt, "index of the last file to init (inclusive). Will init to the end of declared space if not provided.")
	flag.Parse()

	// A workaround to simulate an optional value w/o a default ¯\_(ツ)_/¯
	// The default will be known later, after parsing the flags.
	if to != math.MaxInt {
		opts.ToFileIdx = &to
	}
	opts.NumUnits = uint32(*numUnits) // workaround the missing type support for uint32
}

func processFlags() error {
	if opts.ProviderID == "" {
		return errors.New("-provider flag is required")
	}

	if commitmentAtxIdHex == "" {
		return errors.New("-commitmentAtxId flag is required")
	}
	var err error
	commitmentAtxId, err = hex.DecodeString(commitmentAtxIdHex)
	if err != nil {
		return fmt.Errorf("invalid commitmentAtxId: %w", err)
	}

	if idHex == "" {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			return fmt.Errorf("failed to generate identity: %w", err)
		}
		id = pub
		log.Printf("cli: generated id %x\n", id)
		return saveKey(priv)
	}
	id, err = hex.DecodeString(idHex)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	return nil
}

func main() {
	log.Println(
		"\n************************************************\n" +
			"*      welcome to use spacemesh post tool      *\n" +
			"*      this is a multi-threading version       *\n" +
			"*    https://github.com/fourierism/post.git    *\n" +
			"************************************************")
	parseFlags()

	if printProviders {
		providers, err := postrs.OpenCLProviders()
		if err != nil {
			log.Fatalln("failed to get OpenCL providers", err)
		}
		spew.Dump(providers)
		return
	}

	if printNumFiles {
		totalFiles := opts.TotalFiles(cfg.LabelsPerUnit)
		fmt.Println(totalFiles)
		return
	}

	if printConfig {
		spew.Dump(cfg)
		spew.Dump(opts)
		return
	}

	if err := processFlags(); err != nil {
		log.Fatalln("failed to process flags", err)
	}

	zapLog, err := zap.NewProduction()
	if err != nil {
		log.Fatalln("failed to initialize zap logger:", err)
	}

	providers, err := postrs.OpenCLProviders()
	if err != nil {
		log.Fatalln("failed to get OpenCL providers", err)
	}
	log.Println("providers: ", providers)

	results := make(chan int, 100)
	totalFiles := opts.TotalFiles(cfg.LabelsPerUnit)

	ProviderIDs := strings.Split(opts.ProviderID, ",")
	ProviderIDs_len := len(ProviderIDs)
	each_Files := totalFiles / ProviderIDs_len

	for w := 0; w < ProviderIDs_len; w++ {
		opts.FromFileIdx = w * each_Files
		if w == ProviderIDs_len-1 {
			var i = totalFiles - 1
			opts.ToFileIdx = &i

		} else {
			var i = (w+1)*each_Files - 1
			opts.ToFileIdx = &i
		}

		opts.ProviderID = ProviderIDs[w]
		log.Println("provider:", opts.ProviderID, "-> opts: ", opts)
		go do(zapLog, opts, w, results)
		time.Sleep(time.Second)
	}

	for a := 0; a < ProviderIDs_len; a++ {
		<-results
	}

}

func do(zapLog *zap.Logger, opts config.InitOpts, id_ int, results chan<- int) {

	init, err := initialization.NewInitializer(
		initialization.WithConfig(cfg),
		initialization.WithInitOpts(opts),
		initialization.WithNodeId(id),
		initialization.WithCommitmentAtxId(commitmentAtxId),
		initialization.WithLogger(zapLog),
	)
	if err != nil {
		log.Panic(err.Error())
	}

	if reset {
		if err := init.Reset(); err != nil {
			log.Fatalln("reset error", err)
		}
		log.Println("cli: reset completed")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err = init.Initialize(ctx)
	switch {
	case errors.Is(err, shared.ErrInitCompleted):
		log.Panic(err.Error())
		return
	case errors.Is(err, context.Canceled):
		log.Println("cli: initialization interrupted")
		return
	case err != nil:
		log.Println("cli: initialization error", err)
		return
	}

	log.Println("cli: initialization completed")

	if genProof {
		log.Println("cli: generating proof as a sanity test")

		proof, proofMetadata, err := proving.Generate(ctx, shared.ZeroChallenge, cfg, zapLog, proving.WithDataSource(cfg, id, commitmentAtxId, opts.DataDir))
		if err != nil {
			log.Fatalln("proof generation error", err)
		}
		verifier, err := verifying.NewProofVerifier()
		if err != nil {
			log.Fatalln("failed to create verifier", err)
		}
		defer verifier.Close()
		if err := verifier.Verify(proof, proofMetadata, cfg, zapLog); err != nil {
			log.Fatalln("failed to verify test proof", err)
		}

		log.Println("cli: proof is valid")
	}
}

func saveKey(key ed25519.PrivateKey) error {
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir error: %w", err)
	}

	filename := filepath.Join(opts.DataDir, edKeyFileName)
	if err := os.WriteFile(filename, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return fmt.Errorf("key write to disk error: %w", err)
	}
	return nil
}
