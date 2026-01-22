package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 02-block-ops.go
// 查询最新区块、指定区块以及批量查询区块范围的信息。
//
// 使用示例：
//
//	# 查询最新区块
//	go run main.go
//
//	# 查询指定区块
//	go run main.go -number 123456
//
//	# 批量查询区块范围 [100, 105]
//	go run main.go -range-start 100 -range-end 105
//
//	# 批量查询，自定义请求间隔（毫秒）
//	go run main.go -range-start 100 -range-end 105 -rate-limit 500
func main() {
	blockNumberFlag := flag.Uint64("number", 0, "block number to query (0 means skip)")
	rangeStartFlag := flag.Uint64("range-start", 0, "start block number for range query")
	rangeEndFlag := flag.Uint64("range-end", 0, "end block number for range query")
	rateLimitFlag := flag.Int("rate-limit", 200, "rate limit in milliseconds between requests")
	flag.Parse()

	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("ETH_RPC_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatalf("failed to connect to Ethereum node: %v", err)
	}
	defer client.Close()

	// 最新区块
	latestBlock, err := client.BlockByNumber(ctx, nil)
	if err != nil {
		log.Fatalf("failed to get latest block: %v", err)
	}

	printBlockInfo("Latest Block", latestBlock)

	// 指定区块
	if *blockNumberFlag > 0 {
		num := big.NewInt(0).SetUint64(*blockNumberFlag)
		block, err := fetchBlockWithRetry(ctx, client, num, 3)
		if err != nil {
			log.Fatalf("failed to get block %d: %v", *blockNumberFlag, err)
		}
		printBlockInfo(fmt.Sprintf("Block %d", *blockNumberFlag), block)
	}

	// 批量查询区块范围
	if *rangeStartFlag > 0 && *rangeEndFlag > 0 {
		if *rangeStartFlag > *rangeEndFlag {
			log.Fatal("range-start must be <= range-end")
		}
		rateLimit := time.Duration(*rateLimitFlag) * time.Millisecond
		fetchBlockRange(ctx, client, *rangeStartFlag, *rangeEndFlag, rateLimit)
	}
}

// fetchBlockWithRetry 带重试机制的区块查询
func fetchBlockWithRetry(ctx context.Context, client *ethclient.Client, blockNumber *big.Int, maxRetries int) (*types.Block, error) {
	var lastErr error
	for i := range maxRetries {
		// 每次重试使用新的超时上下文，避免上下文被取消
		reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		block, err := client.BlockByNumber(reqCtx, blockNumber)
		cancel()

		if err == nil {
			return block, nil
		}

		lastErr = err
		if i < maxRetries-1 {
			backoff := time.Duration(1<<i) * 500 * time.Millisecond
			log.Printf("[WARN] failed to fetch block %s, retry %d/%d after %v: %v",
				blockNumber.String(), i+1, maxRetries, backoff, err)
			time.Sleep(backoff)
		}
	}
	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

// fetchBlockRange 批量查询区块范围，带频率控制
func fetchBlockRange(ctx context.Context, client *ethclient.Client, start, end uint64, rateLimit time.Duration) {
	fmt.Printf("\n=== Fetching Block Range [%d, %d] ===\n", start, end)
	fmt.Printf("Rate Limit: %v per request\n\n", rateLimit)

	successCount := 0
	skipCount := 0
	ticker := time.NewTicker(rateLimit)
	defer ticker.Stop()

	for num := start; num <= end; num++ {
		// 等待速率限制
		<-ticker.C

		blockNumber := big.NewInt(0).SetUint64(num)
		block, err := fetchBlockWithRetry(ctx, client, blockNumber, 2)

		if err != nil {
			log.Printf("[ERROR] Block %d: %v", num, err)
			skipCount++
			continue
		}

		successCount++
		printBlockInfo(fmt.Sprintf("Block %d", num), block)

		// 检查上下文是否已取消
		select {
		case <-ctx.Done():
			log.Printf("[INFO] Context cancelled, stopping at block %d", num)
			return
		default:
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Success: %d blocks\n", successCount)
	fmt.Printf("Skipped: %d blocks\n", skipCount)
	fmt.Printf("Total: %d blocks\n", end-start+1)
}

// printBlockInfo 打印详细的区块信息
func printBlockInfo(title string, block *types.Block) {
	fmt.Println("======================================")
	fmt.Println(title)
	fmt.Println("======================================")
	fmt.Printf("Block: %+v\n", block)

	// 基本信息
	fmt.Printf("Number       : %d\n", block.Number().Uint64())
	fmt.Printf("Hash         : %s\n", block.Hash().Hex())
	fmt.Printf("Parent Hash  : %s\n", block.ParentHash().Hex())

	// 时间信息
	blockTime := time.Unix(int64(block.Time()), 0)
	fmt.Printf("Time         : %s\n", blockTime.Format(time.RFC3339))
	fmt.Printf("Time (Local) : %s\n", blockTime.Local().Format("2006-01-02 15:04:05 MST"))

	// Gas 信息
	gasUsed := block.GasUsed()
	gasLimit := block.GasLimit()
	gasUsagePercent := float64(gasUsed) / float64(gasLimit) * 100
	fmt.Printf("Gas Used     : %d (%.2f%%)\n", gasUsed, gasUsagePercent)
	fmt.Printf("Gas Limit    : %d\n", gasLimit)

	// 交易信息
	txCount := len(block.Transactions())
	fmt.Printf("Tx Count     : %d\n", txCount)

	// 区块根信息（Merkle 树根）
	fmt.Printf("State Root   : %s\n", block.Root().Hex())
	fmt.Printf("Tx Root      : %s\n", block.TxHash().Hex())
	fmt.Printf("Receipt Root : %s\n", block.ReceiptHash().Hex())

	// 区块大小估算（简化版，实际大小还包括其他字段）
	if txCount > 0 {
		fmt.Printf("\nFirst Tx Hash: %s\n", block.Transactions()[0].Hash().Hex())
		if txCount > 1 {
			fmt.Printf("Last Tx Hash : %s\n", block.Transactions()[txCount-1].Hash().Hex())
		}
	}

	// 难度信息（PoW 相关，PoS 后基本固定）
	fmt.Printf("Difficulty   : %s\n", block.Difficulty().String())

	// 区块奖励相关信息
	coinbase := block.Coinbase()
	if coinbase != (common.Address{}) {
		fmt.Printf("Coinbase     : %s\n", coinbase.Hex())
	}

	fmt.Println("======================================")
	fmt.Println()
}
