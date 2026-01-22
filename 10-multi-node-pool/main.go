package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 本示例演示一个“简单连接池与多节点策略”：
// - 多个 ethclient.Client 连接不同节点
// - 读操作做简单负载均衡（轮询）
// - 写操作固定主节点（主节点挂了再切换）
// - 节点不可用时自动标记失效并输出告警日志
//
// 使用方式：
//   export ETH_RPC_URLS="http://127.0.0.1:8545,https://sepolia.infura.io/v3/<project-id>"
//   go run main.go

// NodeStatus 表示单个节点的状态
type NodeStatus struct {
	URL    string
	Client *ethclient.Client
	Alive  bool
}

// EthClientPool 简单连接池
type EthClientPool struct {
	mu sync.RWMutex

	nodes []*NodeStatus

	// 写主节点索引（默认 0）
	primaryIdx int

	// 读操作轮询索引
	readIdx int
}

// NewEthClientPool 根据多个 RPC URL 初始化连接池
func NewEthClientPool(ctx context.Context, urls []string) (*EthClientPool, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("no rpc urls provided")
	}

	nodes := make([]*NodeStatus, 0, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue
		}

		client, err := ethclient.DialContext(ctx, u)
		if err != nil {
			log.Printf("[WARN] connect rpc failed, url=%s, err=%v", u, err)
			nodes = append(nodes, &NodeStatus{
				URL:    u,
				Client: nil,
				Alive:  false,
			})
			continue
		}

		log.Printf("[INFO] connected rpc node: %s", u)
		nodes = append(nodes, &NodeStatus{
			URL:    u,
			Client: client,
			Alive:  true,
		})
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no node connected successfully")
	}

	p := &EthClientPool{
		nodes:      nodes,
		primaryIdx: 0,
		readIdx:    0,
	}

	return p, nil
}

// pickReadNode 轮询选择一个可用节点
func (p *EthClientPool) pickReadNode() *NodeStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.nodes)
	for i := 0; i < n; i++ {
		idx := (p.readIdx + i) % n
		node := p.nodes[idx]
		if node.Alive && node.Client != nil {
			p.readIdx = (idx + 1) % n
			return node
		}
	}
	return nil
}

// pickPrimaryNode 选择当前写主节点（如挂了则尝试切换）
func (p *EthClientPool) pickPrimaryNode() *NodeStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	n := len(p.nodes)

	// 先看当前 primary 是否可用
	if n > 0 && p.primaryIdx < n {
		node := p.nodes[p.primaryIdx]
		if node.Alive && node.Client != nil {
			return node
		}
	}

	// 否则从头找一个可用的，顺便更新 primaryIdx
	for i := 0; i < n; i++ {
		node := p.nodes[i]
		if node.Alive && node.Client != nil {
			log.Printf("[WARN] switch primary node to %s", node.URL)
			p.primaryIdx = i
			return node
		}
	}
	return nil
}

// markNodeDead 标记节点不可用
func (p *EthClientPool) markNodeDead(url string, cause error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, node := range p.nodes {
		if node.URL == url {
			if node.Alive {
				log.Printf("[ERROR] mark node dead, url=%s, err=%v", url, cause)
			}
			node.Alive = false
			return
		}
	}
}

// GetLatestBlockNumber 读操作：获取最新区块号（简单读负载均衡）
func (p *EthClientPool) GetLatestBlockNumber(ctx context.Context) (*big.Int, error) {
	node := p.pickReadNode()
	if node == nil {
		return nil, fmt.Errorf("no alive node for read")
	}

	number, err := node.Client.BlockNumber(ctx)
	if err != nil {
		p.markNodeDead(node.URL, err)
		return nil, err
	}

	return new(big.Int).SetUint64(number), nil
}

// GetBalance 读操作示例：查余额
func (p *EthClientPool) GetBalance(ctx context.Context, addr common.Address) (*big.Int, error) {
	node := p.pickReadNode()
	if node == nil {
		return nil, fmt.Errorf("no alive node for read")
	}

	bal, err := node.Client.BalanceAt(ctx, addr, nil)
	if err != nil {
		p.markNodeDead(node.URL, err)
		return nil, err
	}
	return bal, nil
}

// SendDummyWrite 写操作示例：通过主节点发送“写请求”
// 这里不真正发交易，只是展示如何选用主节点。
func (p *EthClientPool) SendDummyWrite(ctx context.Context) error {
	_ = ctx

	node := p.pickPrimaryNode()
	if node == nil {
		return fmt.Errorf("no alive node for write")
	}

	log.Printf("[INFO] perform write operation via primary node: %s", node.URL)
	// 真实场景中，这里会调用：
	//   client.SendTransaction(ctx, signedTx)
	// 或其他写操作。
	return nil
}

func main() {
	rpcURLsEnv := os.Getenv("ETH_RPC_URLS")
	if rpcURLsEnv == "" {
		log.Fatal("ETH_RPC_URLS is not set (example: http://127.0.0.1:8545,https://sepolia.infura.io/v3/<project-id>)")
	}

	urls := strings.Split(rpcURLsEnv, ",")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := NewEthClientPool(ctx, urls)
	if err != nil {
		log.Fatalf("failed to init client pool: %v", err)
	}

	fmt.Println("=== Multi Node Pool Demo ===")
	fmt.Printf("Configured RPC URLs:\n")
	for _, u := range urls {
		fmt.Printf("  - %s\n", strings.TrimSpace(u))
	}
	fmt.Println("============================")

	// 示例 1：多次获取最新区块号，演示读负载均衡（轮询不同节点）
	for i := 0; i < 3; i++ {
		num, err := pool.GetLatestBlockNumber(ctx)
		if err != nil {
			log.Printf("[READ] get latest block failed: %v", err)
			continue
		}
		log.Printf("[READ] latest block number: %s", num.String())
	}

	// 示例 2：查询一个地址余额（这里使用 0 地址，仅做演示）
	addr := common.HexToAddress("0x0000000000000000000000000000000000000000")
	bal, err := pool.GetBalance(ctx, addr)
	if err != nil {
		log.Printf("[READ] get balance failed: %v", err)
	} else {
		log.Printf("[READ] balance of %s: %s wei", addr.Hex(), bal.String())
	}

	// 示例 3：写操作通过主节点执行
	if err := pool.SendDummyWrite(ctx); err != nil {
		log.Printf("[WRITE] write operation failed: %v", err)
	}
}
