package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 01-subscribe-blocks.go
// 通过 SubscribeNewHead 订阅新区块头。
// 注意：大多数节点要求使用 WebSocket RPC，例如：ws://127.0.0.1:8546 或 wss://...
func main() {
	rpcURL := os.Getenv("ETH_WS_URL")
	if rpcURL == "" {
		// 回退到 ETH_RPC_URL，便于在只配置了 HTTP 的环境中看到错误提示
		rpcURL = os.Getenv("ETH_RPC_URL")
	}
	if rpcURL == "" {
		log.Fatal("ETH_WS_URL or ETH_RPC_URL must be set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatalf("failed to connect to Ethereum node: %v", err)
	}
	defer client.Close()

	headers := make(chan *types.Header)
	sub, err := client.SubscribeNewHead(ctx, headers)
	if err != nil {
		log.Fatalf("failed to subscribe new heads: %v", err)
	}

	fmt.Printf("Subscribed to new blocks via %s\n", rpcURL)

	// 捕获 Ctrl+C 退出
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case h := <-headers:
			if h == nil {
				continue
			}
			fmt.Printf("[%s] New Block - Number: %d, Hash: %s\n",
				time.Now().Format(time.RFC3339),
				h.Number.Uint64(),
				h.Hash().Hex(),
			)
		case err := <-sub.Err():
			log.Printf("subscription error: %v", err)
			return
		case sig := <-sigCh:
			fmt.Printf("received signal %s, shutting down...\n", sig.String())
			return
		case <-ctx.Done():
			fmt.Println("context cancelled, exiting...")
			return
		}
	}
}
