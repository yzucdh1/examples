package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"log"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 08-contract-interact.go
// 使用通用 ABI 调用 ERC-20 合约的方法，包括：
// 1. balanceOf: 查询余额（只读调用）
// 2. transfer: 发送 ERC-20 转账交易（需要设置 SENDER_PRIVATE_KEY 环境变量）
// 3. parse-event: 从交易回执中解析 Transfer 事件，展示 indexed 参数和 data 的对应关系
//
// 执行示例：
//
// 1. 查询 ERC-20 代币余额：
//    export ETH_RPC_URL="http://127.0.0.1:8545"
//    go run main.go --mode balance \
//      --contract 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
//      --address 0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb
//
// 2. 发送 ERC-20 转账交易（使用代币数量，自动根据 decimals 转换）：
//    export ETH_RPC_URL="http://127.0.0.1:8545"
//    export SENDER_PRIVATE_KEY="your_private_key_hex"
//    go run main.go --mode transfer \
//      --contract 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
//      --to 0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb \
//      --amount 1.5
//
// 3. 发送 ERC-20 转账交易（使用代币的最小单位）：
//    export ETH_RPC_URL="http://127.0.0.1:8545"
//    export SENDER_PRIVATE_KEY="your_private_key_hex"
//    go run main.go --mode transfer \
//      --contract 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
//      --to 0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb \
//      --amount 1500000
//
// 4. 解析交易中的 Transfer 事件：
//    export ETH_RPC_URL="http://127.0.0.1:8545"
//    go run main.go --mode parse-event \
//      --tx 0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef
//
// 注意事项：
// - 所有示例中的地址和交易哈希都是示例，请替换为实际值
// - transfer 模式需要设置 SENDER_PRIVATE_KEY 环境变量（私钥十六进制，可带或不带 0x 前缀）
// - 仅在测试网或本地开发链上使用，不要在主网使用包含真实资产的私钥
// - amount 参数支持两种格式：
//   * 小数格式（如 "1.5"）：自动根据代币的 decimals 转换为最小单位
//   * 整数格式（如 "1500000"）：直接作为代币的最小单位使用

const erc20ABIJSON = `[
  {
    "constant": true,
    "inputs": [{"name": "owner", "type": "address"}],
    "name": "balanceOf",
    "outputs": [{"name": "balance", "type": "uint256"}],
    "type": "function"
  },
  {
    "constant": true,
    "inputs": [],
    "name": "decimals",
    "outputs": [{"name": "", "type": "uint8"}],
    "type": "function"
  },
  {
    "constant": false,
    "inputs": [
      {"name": "to", "type": "address"},
      {"name": "value", "type": "uint256"}
    ],
    "name": "transfer",
    "outputs": [{"name": "", "type": "bool"}],
    "type": "function"
  },
  {
    "anonymous": false,
    "inputs": [
      {"indexed": true, "name": "from", "type": "address"},
      {"indexed": true, "name": "to", "type": "address"},
      {"indexed": false, "name": "value", "type": "uint256"}
    ],
    "name": "Transfer",
    "type": "event"
  }
]`

func main() {
	mode := flag.String("mode", "balance", "operation mode: balance, transfer, or parse-event")
	contractHex := flag.String("contract", "", "ERC-20 contract address")
	addrHex := flag.String("address", "", "address (for balanceOf or transfer to)")
	toHex := flag.String("to", "", "recipient address (for transfer)")
	amount := flag.String("amount", "", "transfer amount (for transfer, can be token amount like 1.5 or raw amount)")
	txHashHex := flag.String("tx", "", "transaction hash (for parse-event)")
	flag.Parse()

	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("ETH_RPC_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatalf("failed to connect to Ethereum node: %v", err)
	}
	defer client.Close()

	parsedABI, err := abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		log.Fatalf("failed to parse ABI: %v", err)
	}

	switch *mode {
	case "balance":
		handleBalanceOf(ctx, client, parsedABI, *contractHex, *addrHex)
	case "transfer":
		handleTransfer(ctx, client, parsedABI, *contractHex, *toHex, *amount)
	case "parse-event":
		handleParseEvent(ctx, client, parsedABI, *txHashHex)
	default:
		log.Fatalf("unknown mode: %s (use: balance, transfer, or parse-event)", *mode)
	}
}

// handleBalanceOf 查询 ERC-20 代币余额
func handleBalanceOf(ctx context.Context, client *ethclient.Client, parsedABI abi.ABI, contractHex, addrHex string) {
	if contractHex == "" || addrHex == "" {
		log.Fatal("missing --contract or --address flag for balance mode")
	}

	contractAddr := common.HexToAddress(contractHex)
	targetAddr := common.HexToAddress(addrHex)

	// 编码 balanceOf 调用数据
	data, err := parsedABI.Pack("balanceOf", targetAddr)
	if err != nil {
		log.Fatalf("failed to pack data: %v", err)
	}

	callMsg := ethereum.CallMsg{
		To:   &contractAddr,
		Data: data,
	}

	// 执行只读调用
	output, err := client.CallContract(ctx, callMsg, nil)
	if err != nil {
		log.Fatalf("CallContract error: %v", err)
	}

	// 解码返回值
	var balance *big.Int
	err = parsedABI.UnpackIntoInterface(&balance, "balanceOf", output)
	if err != nil {
		log.Fatalf("failed to unpack output: %v", err)
	}

	fmt.Printf("Contract : %s\n", contractAddr.Hex())
	fmt.Printf("Address  : %s\n", targetAddr.Hex())
	fmt.Printf("Balance  : %s (raw uint256)\n", balance.String())
}

// handleTransfer 发送 ERC-20 transfer 交易
func handleTransfer(ctx context.Context, client *ethclient.Client, parsedABI abi.ABI, contractHex, toHex, amountStr string) {
	if contractHex == "" || toHex == "" || amountStr == "" {
		log.Fatal("missing --contract, --to, or --amount flag for transfer mode")
	}

	// 检查私钥环境变量
	privKeyHex := os.Getenv("SENDER_PRIVATE_KEY")
	if privKeyHex == "" {
		log.Fatal("SENDER_PRIVATE_KEY is not set (required for transfer mode)")
	}

	// 解析私钥
	privKey, err := crypto.HexToECDSA(trim0x(privKeyHex))
	if err != nil {
		log.Fatalf("invalid private key: %v", err)
	}

	// 获取发送方地址
	publicKey := privKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Fatal("error casting public key to ECDSA")
	}
	fromAddr := crypto.PubkeyToAddress(*publicKeyECDSA)

	contractAddr := common.HexToAddress(contractHex)
	toAddr := common.HexToAddress(toHex)

	// 查询代币的 decimals（精度）
	decimals, err := getTokenDecimals(ctx, client, parsedABI, contractAddr)
	if err != nil {
		log.Fatalf("failed to get token decimals: %v", err)
	}

	// 解析转账金额
	// 如果输入包含小数点，则认为是代币数量，需要根据 decimals 转换
	// 如果输入是整数，则认为是代币的最小单位（类似 wei 的概念）
	amount, err := parseTokenAmount(amountStr, decimals)
	if err != nil {
		log.Fatalf("invalid amount: %v", err)
	}

	// 获取链 ID
	chainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("failed to get chain id: %v", err)
	}

	// 获取 nonce
	nonce, err := client.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		log.Fatalf("failed to get nonce: %v", err)
	}

	// 编码 transfer 调用数据
	// transfer(address to, uint256 value)
	callData, err := parsedABI.Pack("transfer", toAddr, amount)
	if err != nil {
		log.Fatalf("failed to pack transfer data: %v", err)
	}

	// 估算 Gas Limit（合约调用需要更多 Gas）
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddr,
		To:   &contractAddr,
		Data: callData,
	})
	if err != nil {
		log.Fatalf("failed to estimate gas: %v", err)
	}
	// 增加 20% 的缓冲，避免 Gas 不足
	gasLimit = gasLimit * 120 / 100

	// 获取建议的 Gas 价格（使用 EIP-1559 动态费用）
	gasTipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		log.Fatalf("failed to get gas tip cap: %v", err)
	}

	// 获取 base fee，计算 fee cap
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		log.Fatalf("failed to get header: %v", err)
	}

	baseFee := header.BaseFee
	if baseFee == nil {
		// 如果不支持 EIP-1559，使用传统 gas price
		gasPrice, err := client.SuggestGasPrice(ctx)
		if err != nil {
			log.Fatalf("failed to get gas price: %v", err)
		}
		baseFee = gasPrice
	}

	// fee cap = base fee * 2 + tip cap（简单策略）
	gasFeeCap := new(big.Int).Add(
		new(big.Int).Mul(baseFee, big.NewInt(2)),
		gasTipCap,
	)

	// 检查 ETH 余额是否足够支付 Gas 费用
	balance, err := client.BalanceAt(ctx, fromAddr, nil)
	if err != nil {
		log.Fatalf("failed to get balance: %v", err)
	}

	// 计算总费用：gasFeeCap * gasLimit（ERC-20 转账不需要发送 ETH，只需要支付 Gas）
	totalGasCost := new(big.Int).Mul(gasFeeCap, big.NewInt(int64(gasLimit)))

	if balance.Cmp(totalGasCost) < 0 {
		log.Fatalf("insufficient ETH balance for gas: have %s wei, need %s wei", balance.String(), totalGasCost.String())
	}

	// 构造交易（EIP-1559 动态费用交易）
	// 注意：ERC-20 transfer 的 value 为 0，调用数据在 Data 字段中
	txData := &types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &contractAddr, // 合约地址
		Value:     big.NewInt(0), // ERC-20 转账不需要发送 ETH
		Data:      callData,      // transfer 调用数据
	}
	tx := types.NewTx(txData)

	// 签名交易
	signer := types.NewLondonSigner(chainID)
	signedTx, err := types.SignTx(tx, signer, privKey)
	if err != nil {
		log.Fatalf("failed to sign transaction: %v", err)
	}

	// 发送交易
	if err := client.SendTransaction(ctx, signedTx); err != nil {
		log.Fatalf("failed to send transaction: %v", err)
	}

	// 输出交易信息
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("ERC-20 Transfer Transaction Sent\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("From          : %s\n", fromAddr.Hex())
	fmt.Printf("To            : %s\n", toAddr.Hex())
	fmt.Printf("Contract      : %s\n", contractAddr.Hex())
	fmt.Printf("Token Decimals: %d\n", decimals)
	// 显示代币数量（根据 decimals 转换）
	tokenAmount := formatTokenAmount(amount, decimals)
	fmt.Printf("Amount        : %s tokens (%s raw units)\n", tokenAmount, amount.String())
	fmt.Printf("Gas Limit     : %d\n", gasLimit)
	fmt.Printf("Gas Tip Cap   : %s Wei\n", gasTipCap.String())
	fmt.Printf("Gas Fee Cap   : %s Wei\n", gasFeeCap.String())
	fmt.Printf("Estimated Cost: %s Wei\n", totalGasCost.String())
	fmt.Printf("Nonce         : %d\n", nonce)
	fmt.Printf("Tx Hash       : %s\n", signedTx.Hash().Hex())
	fmt.Printf("\n")
	fmt.Printf("Transaction is pending. Waiting for confirmation...\n")
	fmt.Printf("\n")

	// 等待交易确认
	waitForTransaction(ctx, client, signedTx.Hash())
}

// waitForTransaction 等待交易确认并显示回执信息
func waitForTransaction(ctx context.Context, client *ethclient.Client, txHash common.Hash) {
	// 设置超时上下文（最多等待 2 分钟）
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	fmt.Printf("Polling for transaction receipt...\n")
	for {
		select {
		case <-waitCtx.Done():
			fmt.Printf("\nTimeout waiting for transaction confirmation.\n")
			fmt.Printf("You can check the transaction status later:\n")
			fmt.Printf("  go run main.go --mode parse-event --tx %s\n", txHash.Hex())
			return

		case <-ticker.C:
			receipt, err := client.TransactionReceipt(waitCtx, txHash)
			if err != nil {
				// 交易可能还在 pending
				continue
			}

			// 交易已确认
			fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
			fmt.Printf("Transaction Confirmed!\n")
			fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
			fmt.Printf("Status       : %d (1=success, 0=failed)\n", receipt.Status)
			fmt.Printf("Block Number : %d\n", receipt.BlockNumber.Uint64())
			fmt.Printf("Block Hash   : %s\n", receipt.BlockHash.Hex())
			fmt.Printf("Gas Used     : %d / %d\n", receipt.GasUsed, receipt.GasUsed)
			fmt.Printf("Logs Count   : %d\n", len(receipt.Logs))

			if receipt.Status == 0 {
				fmt.Printf("\n⚠️  Transaction failed! Check the transaction on block explorer.\n")
			} else {
				fmt.Printf("\n✅ Transaction successful!\n")
				if len(receipt.Logs) > 0 {
					fmt.Printf("\nTo parse Transfer event from this transaction:\n")
					fmt.Printf("  go run main.go --mode parse-event --tx %s\n", txHash.Hex())
				}
			}
			fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
			return
		}
	}
}

// trim0x 移除十六进制字符串前缀 "0x"
func trim0x(s string) string {
	if len(s) >= 2 && s[0:2] == "0x" {
		return s[2:]
	}
	return s
}

// getTokenDecimals 查询 ERC-20 代币的 decimals（精度）
func getTokenDecimals(ctx context.Context, client *ethclient.Client, parsedABI abi.ABI, contractAddr common.Address) (uint8, error) {
	// 编码 decimals() 调用数据
	data, err := parsedABI.Pack("decimals")
	if err != nil {
		return 0, fmt.Errorf("failed to pack decimals data: %w", err)
	}

	callMsg := ethereum.CallMsg{
		To:   &contractAddr,
		Data: data,
	}

	// 执行只读调用
	output, err := client.CallContract(ctx, callMsg, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to call decimals: %w", err)
	}

	// 解码返回值
	var decimals uint8
	err = parsedABI.UnpackIntoInterface(&decimals, "decimals", output)
	if err != nil {
		return 0, fmt.Errorf("failed to unpack decimals output: %w", err)
	}

	return decimals, nil
}

// parseTokenAmount 解析代币数量字符串
// 如果输入包含小数点（如 "1.5"），则认为是代币数量，需要根据 decimals 转换为最小单位
// 如果输入是整数（如 "1500000000000000000"），则认为是代币的最小单位（类似 wei 的概念）
func parseTokenAmount(amountStr string, decimals uint8) (*big.Int, error) {
	// 检查是否包含小数点
	if strings.Contains(amountStr, ".") {
		// 解析为浮点数
		amountFloat, err := strconv.ParseFloat(amountStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid decimal amount: %w", err)
		}

		// 转换为 big.Float 进行精确计算
		bigFloat := big.NewFloat(amountFloat)

		// 乘以 10^decimals
		multiplier := new(big.Float).SetFloat64(math.Pow10(int(decimals)))
		bigFloat.Mul(bigFloat, multiplier)

		// 转换为 big.Int（截断小数部分）
		amount, _ := bigFloat.Int(nil)
		return amount, nil
	} else {
		// 直接解析为整数（代币的最小单位）
		amount, ok := new(big.Int).SetString(amountStr, 10)
		if !ok {
			return nil, fmt.Errorf("invalid integer amount: %s", amountStr)
		}
		return amount, nil
	}
}

// formatTokenAmount 将代币的最小单位转换为可读的代币数量
func formatTokenAmount(amount *big.Int, decimals uint8) string {
	// 转换为 big.Float
	amountFloat := new(big.Float).SetInt(amount)

	// 除以 10^decimals
	divisor := new(big.Float).SetFloat64(math.Pow10(int(decimals)))
	amountFloat.Quo(amountFloat, divisor)

	// 格式化为字符串，保留足够的小数位
	return amountFloat.Text('f', int(decimals))
}

// handleParseEvent 从交易回执中解析 Transfer 事件
// 详细展示 indexed 参数（存储在 Topics 中）和 non-indexed 参数（存储在 Data 中）的对应关系
func handleParseEvent(ctx context.Context, client *ethclient.Client, parsedABI abi.ABI, txHashHex string) {
	if txHashHex == "" {
		log.Fatal("missing --tx flag for parse-event mode")
	}

	txHash := common.HexToHash(txHashHex)

	// 获取交易回执
	receipt, err := client.TransactionReceipt(ctx, txHash)
	if err != nil {
		log.Fatalf("failed to get transaction receipt: %v", err)
	}

	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Transaction Receipt Analysis\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Tx Hash      : %s\n", txHash.Hex())
	fmt.Printf("Block Number : %d\n", receipt.BlockNumber.Uint64())
	fmt.Printf("Status       : %d (1=success, 0=failed)\n", receipt.Status)
	fmt.Printf("Gas Used     : %d\n", receipt.GasUsed)
	fmt.Printf("Logs Count   : %d\n", len(receipt.Logs))
	fmt.Printf("\n")

	// 查找 Transfer 事件
	transferEvent := parsedABI.Events["Transfer"]
	transferEventSigHash := crypto.Keccak256Hash([]byte(transferEvent.Sig))

	foundTransfer := false
	for i, vLog := range receipt.Logs {
		// 检查是否是 Transfer 事件（通过 Topics[0] 匹配事件签名哈希）
		if len(vLog.Topics) == 0 || vLog.Topics[0] != transferEventSigHash {
			continue
		}

		foundTransfer = true
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Transfer Event #%d\n", i+1)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Contract Address: %s\n", vLog.Address.Hex())
		fmt.Printf("Log Index       : %d\n", vLog.Index)
		fmt.Printf("\n")

		// ============================================================
		// 事件存储结构说明
		// ============================================================
		fmt.Printf("Event Storage Structure:\n")
		fmt.Printf("────────────────────────────────────────────────────────\n")
		fmt.Printf("Transfer(address indexed from, address indexed to, uint256 value)\n")
		fmt.Printf("\n")
		fmt.Printf("事件参数存储位置：\n")
		fmt.Printf("  • Topics[0]: 事件签名哈希 (Event Signature Hash)\n")
		fmt.Printf("  • Topics[1]: from (indexed address) - 存储在 Topics 中\n")
		fmt.Printf("  • Topics[2]: to (indexed address) - 存储在 Topics 中\n")
		fmt.Printf("  • Data     : value (non-indexed uint256) - 存储在 Data 中\n")
		fmt.Printf("\n")
		fmt.Printf("为什么这样存储？\n")
		fmt.Printf("  • indexed 参数：可以用于事件过滤和搜索，存储在 Topics 中\n")
		fmt.Printf("  • non-indexed 参数：完整数据存储在 Data 中，使用 ABI 编码\n")
		fmt.Printf("  • Topics 最多 4 个元素，因此最多 3 个 indexed 参数\n")
		fmt.Printf("────────────────────────────────────────────────────────\n")
		fmt.Printf("\n")

		// ============================================================
		// 解析 Topics
		// ============================================================
		fmt.Printf("Topics (Indexed Parameters):\n")
		fmt.Printf("────────────────────────────────────────────────────────\n")
		fmt.Printf("Topics Count: %d\n", len(vLog.Topics))
		fmt.Printf("\n")

		// Topics[0]: 事件签名哈希
		fmt.Printf("Topics[0] (Event Signature Hash):\n")
		fmt.Printf("  Hex: %s\n", vLog.Topics[0].Hex())
		fmt.Printf("  Event: Transfer(address,address,uint256)\n")
		fmt.Printf("  Signature: %s\n", transferEvent.Sig)
		fmt.Printf("\n")

		// Topics[1]: from (indexed address)
		if len(vLog.Topics) >= 2 {
			fmt.Printf("Topics[1] (from - indexed address):\n")
			fmt.Printf("  Raw Hex: %s\n", vLog.Topics[1].Hex())
			fmt.Printf("  Explanation: address 类型在 topic 中是 32 字节，前 12 字节为 0 填充\n")
			// 解析 address：去除前 12 字节的 0 填充，后 20 字节是地址
			fromAddr := common.BytesToAddress(vLog.Topics[1].Bytes())
			fmt.Printf("  Parsed Address: %s\n", fromAddr.Hex())
			fmt.Printf("\n")
		}

		// Topics[2]: to (indexed address)
		if len(vLog.Topics) >= 3 {
			fmt.Printf("Topics[2] (to - indexed address):\n")
			fmt.Printf("  Raw Hex: %s\n", vLog.Topics[2].Hex())
			fmt.Printf("  Explanation: address 类型在 topic 中是 32 字节，前 12 字节为 0 填充\n")
			// 解析 address：去除前 12 字节的 0 填充，后 20 字节是地址
			toAddr := common.BytesToAddress(vLog.Topics[2].Bytes())
			fmt.Printf("  Parsed Address: %s\n", toAddr.Hex())
			fmt.Printf("\n")
		}

		// ============================================================
		// 解析 Data
		// ============================================================
		fmt.Printf("Data (Non-Indexed Parameters):\n")
		fmt.Printf("────────────────────────────────────────────────────────\n")
		if len(vLog.Data) > 0 {
			fmt.Printf("Data Length: %d bytes\n", len(vLog.Data))
			fmt.Printf("Raw Hex: 0x%x\n", vLog.Data)
			fmt.Printf("\n")
			fmt.Printf("Data 字段包含所有 non-indexed 参数的 ABI 编码数据\n")
			fmt.Printf("对于 Transfer 事件，Data 中只包含 value (uint256)\n")
			fmt.Printf("\n")

			// 使用 ABI 解码 Data 字段
			// 注意：Unpack 只解码 Data 字段，不包含 Topics 中的 indexed 参数
			values, err := parsedABI.Unpack("Transfer", vLog.Data)
			if err != nil {
				fmt.Printf("Error decoding data: %v\n", err)
			} else {
				fmt.Printf("Decoded Parameters from Data:\n")
				// Transfer 事件只有一个 non-indexed 参数：value
				if len(values) > 0 {
					value, ok := values[0].(*big.Int)
					if ok {
						fmt.Printf("  value (uint256): %s\n", value.String())
						fmt.Printf("  Explanation: uint256 类型直接存储在 Data 中，使用 ABI 编码\n")
					}
				}
			}
		} else {
			fmt.Printf("Data is empty (all parameters are indexed)\n")
		}
		fmt.Printf("\n")

		// ============================================================
		// 完整解析结果
		// ============================================================
		fmt.Printf("Complete Parsed Event:\n")
		fmt.Printf("────────────────────────────────────────────────────────\n")
		if len(vLog.Topics) >= 3 {
			fromAddr := common.BytesToAddress(vLog.Topics[1].Bytes())
			toAddr := common.BytesToAddress(vLog.Topics[2].Bytes())

			var value *big.Int
			if len(vLog.Data) > 0 {
				values, err := parsedABI.Unpack("Transfer", vLog.Data)
				if err == nil && len(values) > 0 {
					if v, ok := values[0].(*big.Int); ok {
						value = v
					}
				}
			}

			if value != nil {
				fmt.Printf("  from  : %s (from Topics[1])\n", fromAddr.Hex())
				fmt.Printf("  to    : %s (from Topics[2])\n", toAddr.Hex())
				fmt.Printf("  value : %s (from Data)\n", value.String())
			}
		}
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("\n")
	}

	if !foundTransfer {
		fmt.Printf("No Transfer event found in this transaction.\n")
		fmt.Printf("Total logs: %d\n", len(receipt.Logs))
	}
}
