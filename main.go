package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// 查询最新区块：
// go run main.go
//
// 查询指定区块：
// go run main.go --number 123456
//
// 发送交易命令：
// go run main.go --send --to 0xRecipientAddress --amount 0.01
func main() {
	sendMode := flag.Bool("send", false, "enable send transaction mode")
	toAddrHex := flag.String("to", "", "recipient address (required for send mode)")
	amountEth := flag.Float64("amount", 0, "amount in ETH (required for send mode)")
	blockNumberFlag := flag.Uint64("number", 0, "block number to query (0 means skip)")
	flag.Parse()

	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("ETH_RPC_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		log.Fatalf("failed to connect to Ethereum node: %v", err)
	}
	defer client.Close()

	if *sendMode {
		// 发送交易
		sendTransaction(client, ctx, *toAddrHex, *amountEth)
	} else {
		if *blockNumberFlag > 0 {
			// 查询指定区块
			queryBlockByNumber(client, ctx, blockNumberFlag)
		} else {
			// 查询最新区块
			queryLatestBlock(client, ctx)
		}
	}
}

// 查询最新区块
func queryLatestBlock(client *ethclient.Client, ctx context.Context) {
	latestBlock, err := client.BlockByNumber(ctx, nil)
	if err != nil {
		log.Fatalf("failed to query latest block: %v", err)
	}

	printBlockInfo("Latest Block", latestBlock)
}

// 查询指定区块
func queryBlockByNumber(client *ethclient.Client, ctx context.Context, blockNumberFlag *uint64) {
	blockNumber := big.NewInt(0).SetUint64(*blockNumberFlag)
	block, err := client.BlockByNumber(ctx, blockNumber)
	if err != nil {
		log.Fatalf("failed to get block %d: %v", *blockNumberFlag, err)
	}
	printBlockInfo(fmt.Sprintf("Block %d", *blockNumberFlag), block)
}

// 发送交易
func sendTransaction(
	client *ethclient.Client,
	ctx context.Context,
	toAddrHex string,
	amountEth float64,
) {
	privKeyHex := os.Getenv("SENDER_PRIVATE_KEY")
	if privKeyHex == "" {
		log.Fatal("SENDER_PRIVATE_KEY is not set (required for send mode)")
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
	toAddr := common.HexToAddress(toAddrHex)

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

	// 估算 Gas Limit（普通转账固定为 21000）
	gasLimit := uint64(21000)

	// 转换 ETH 金额为 Wei
	// amountEth * 1e18
	amountWei := new(big.Float).Mul(
		big.NewFloat(amountEth),
		big.NewFloat(1e18),
	)
	valueWei, _ := amountWei.Int(nil)

	// 检查余额是否足够
	balance, err := client.BalanceAt(ctx, fromAddr, nil)
	if err != nil {
		log.Fatalf("failed to get balance: %v", err)
	}

	// 计算总费用：value + gasFeeCap * gasLimit
	totalCost := new(big.Int).Add(
		valueWei,
		new(big.Int).Mul(gasFeeCap, big.NewInt(int64(gasLimit))),
	)

	if balance.Cmp(totalCost) < 0 {
		log.Fatalf("insufficient balance: have %s wei, need %s wei", balance.String(), totalCost.String())
	}

	// 构造交易（EIP-1559 动态费用交易）
	txData := &types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gasLimit,
		To:        &toAddr,
		Value:     valueWei,
		Data:      nil,
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
	// ETH/SOL原生币转账 - gas费消耗ETH/SOL- ETH是以太坊网络的原生币，转账时需要支付ETH作为gas费；SOL是Solana网络的原生币，转账时需要支付SOL作为gas费。
	// ERC20/ERC721代币转账 - gas费消耗ETH - ERC20代币是以太坊的同质化代币，ERC721代币是以太坊的非同质化代币（NFT），虽然它们代表不同类型的资产，但在以太坊网络上执行这些转账操作时，仍然需要支付ETH作为gas费，因为这些操作是通过智能合约执行的，而智能合约的执行需要消耗以太坊网络的计算资源。
	// ETH/SOL：相对于法币，ETH和SOL是代币；ERC20/ERC721代币：相对于ETH，ERC20和ERC721是代币。
	fmt.Println("=== Transaction Sent ===")
	fmt.Printf("From       : %s\n", fromAddr.Hex())
	fmt.Printf("To         : %s\n", toAddr.Hex())
	fmt.Printf("Value      : %s ETH (%s Wei)\n", fmt.Sprintf("%.6f", amountEth), valueWei.String())
	fmt.Printf("Gas Limit  : %d\n", gasLimit)
	fmt.Printf("Gas Tip Cap: %s Wei\n", gasTipCap.String())
	fmt.Printf("Gas Fee Cap: %s Wei\n", gasFeeCap.String())
	fmt.Printf("Nonce      : %d\n", nonce)
	fmt.Printf("Tx Hash    : %s\n", signedTx.Hash().Hex())
}

// trim0x 移除十六进制字符串前缀 "0x"
func trim0x(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}

// printBlockInfo 打印详细的区块信息
func printBlockInfo(title string, block *types.Block) {
	fmt.Println("======================================")
	fmt.Println(title)
	fmt.Println("======================================")

	// 基本信息
	fmt.Printf("Number       : %d\n", block.Number().Uint64())
	fmt.Printf("Hash         : %s\n", block.Hash().Hex())
	fmt.Printf("Parent Hash  : %s\n", block.ParentHash().Hex())

	// 时间信息
	blockTime := time.Unix(int64(block.Time()), 0)
	fmt.Printf("Time         : %s\n", blockTime.Format(time.RFC3339))
	fmt.Printf("Time (Local) : %s\n", blockTime.Local().Format("2006-01-02 15:04:05 MST"))

	// 交易信息
	txCount := len(block.Transactions())
	fmt.Printf("Tx Count     : %d\n", txCount)

	fmt.Println("======================================")
	fmt.Println()
}
