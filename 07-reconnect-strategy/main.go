package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 04-reconnect-strategy.go
// 展示订阅断线后的简单重连策略（示意实现）。

func main() {
	rpcURL := os.Getenv("ETH_WS_URL")
	if rpcURL == "" {
		rpcURL = os.Getenv("ETH_RPC_URL")
	}
	if rpcURL == "" {
		log.Fatal("ETH_WS_URL or ETH_RPC_URL must be set")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Printf("received signal %s, shutting down...\n", sig.String())
		cancel()
	}()

	runWithReconnect(ctx, rpcURL)
}

func runWithReconnect(ctx context.Context, rpcURL string) {
	var attempt int

	for {
		select {
		case <-ctx.Done():
			fmt.Println("context cancelled, stop reconnect loop")
			return
		default:
		}

		attempt++
		log.Printf("connect attempt #%d to %s", attempt, rpcURL)

		client, err := ethclient.DialContext(ctx, rpcURL)
		if err != nil {
			log.Printf("failed to connect: %v", err)
			sleepWithBackoff(ctx, attempt)
			continue
		}

		headers := make(chan *types.Header)
		sub, err := client.SubscribeNewHead(ctx, headers)
		if err != nil {
			log.Printf("failed to subscribe new heads: %v", err)
			client.Close()
			sleepWithBackoff(ctx, attempt)
			continue
		}

		log.Println("subscription established")

		// 订阅循环：如果 sub.Err() 返回错误，则跳出重新连接
		for {
			select {
			case h := <-headers:
				if h == nil {
					continue
				}
				fmt.Printf("New Block: %d, Hash: %s\n", h.Number.Uint64(), h.Hash().Hex())
			case err := <-sub.Err():
				log.Printf("subscription error: %v", err)
				client.Close()
				sleepWithBackoff(ctx, attempt)
				goto RECONNECT
			case <-ctx.Done():
				log.Println("context cancelled, closing client")
				client.Close()
				return
			}
		}

	RECONNECT:
		// 下一轮 for 循环将尝试重连
	}
}

func sleepWithBackoff(ctx context.Context, attempt int) {
	// 简单指数退避，最大 1 分钟
	sec := int(math.Min(60, math.Pow(2, float64(attempt))))
	d := time.Duration(sec) * time.Second
	log.Printf("will retry in %s", d)

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
