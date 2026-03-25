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

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"task2/counter"
)

// 部署合约
// go run main.go --deploy
//
// 与已部署的合约交互
// go run main.go
func main() {
	deployMode := flag.Bool("deploy", false, "enable send transaction mode")
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

	if *deployMode {
		// 部署合约
		deploy(ctx, client)
	} else {
		// 与已部署的合约交互
		interact(ctx, client)
	}
}

// 部署合约
func deploy(ctx context.Context, client *ethclient.Client) {
	// 准备部署账户（使用私钥）
	privKeyHex := os.Getenv("DEPLOYER_PRIVATE_KEY")
	if privKeyHex == "" {
		log.Fatal("DEPLOYER_PRIVATE_KEY is not set (required for send mode)")
	}

	// 解析私钥
	privateKey, err := crypto.HexToECDSA(trim0x(privKeyHex))
	if err != nil {
		log.Fatalf("invalid private key: %v", err)
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		log.Fatal("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	fromAddress := crypto.PubkeyToAddress(*publicKeyECDSA)

	// 获取链 ID
	chainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("failed to get chain id: %v", err)
	}

	// 获取 nonce
	nonce, err := client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		log.Fatalf("failed to get nonce: %v", err)
	}

	// 获取gas价格建议
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		log.Fatalf("failed to suggest gas price: %v", err)
	}

	// 创建交易签名者
	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(int64(chainID.Int64())))
	if err != nil {
		log.Fatalf("failed to create transactor: %v", err)
	}
	auth.Nonce = big.NewInt(int64(nonce))
	auth.Value = big.NewInt(0)
	auth.GasLimit = uint64(300000)
	auth.GasPrice = gasPrice

	// 部署合约
	address, tx, instance, err := counter.DeployCounter(auth, client)
	if err != nil {
		log.Fatalf("failed to deploy contract: %v", err)
	}

	fmt.Printf("合约地址: %s\n", address.Hex())
	fmt.Printf("交易哈希: %s\n", tx.Hash().Hex())
	fmt.Printf("合约实例: %v\n", instance)
}

// 与已部署的合约交互
func interact(ctx context.Context, client *ethclient.Client) {
	// 合约地址（替换为您的实际合约地址）
	deployedContractAddress := os.Getenv("DEPLOYED_CONTRACT_ADDRESS")
	if deployedContractAddress == "" {
		log.Fatalf("DEPLOYED_CONTRACT_ADDRESS is not set (required for interact mode)")
	}
	contractAddress := common.HexToAddress(deployedContractAddress)

	// 加载已部署的合约
	counter, err := counter.NewCounter(contractAddress, client)
	if err != nil {
		log.Fatalf("failed to load contract: %v", err)
	}

	// 1. 查询当前计数
	count, err := counter.X(&bind.CallOpts{})
	if err != nil {
		log.Fatalf("failed to call contract method: %v", err)
	}
	fmt.Printf("当前计数: %d\n", count)

	// 2. 增加计数（需要签名）
	// 准备部署账户（使用私钥）
	privKeyHex := os.Getenv("DEPLOYER_PRIVATE_KEY")
	if privKeyHex == "" {
		log.Fatal("DEPLOYER_PRIVATE_KEY is not set (required for send mode)")
	}

	// 解析私钥
	privateKey, err := crypto.HexToECDSA(trim0x(privKeyHex))
	if err != nil {
		log.Fatalf("invalid private key: %v", err)
	}

	// 获取链 ID
	chainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("failed to get chain id: %v", err)
	}

	// 创建交易签名者
	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(chainID.Int64()))
	if err != nil {
		log.Fatalf("failed to create transactor: %v", err)
	}

	// 设置gas限制和价格
	auth.GasLimit = uint64(300000)
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		log.Fatalf("failed to suggest gas price: %v", err)
	}
	auth.GasPrice = gasPrice

	// 调用increment方法
	tx, err := counter.Inc(auth)
	if err != nil {
		log.Fatalf("failed to send transaction: %v", err)
	}
	fmt.Printf("交易已发送: %s\n", tx.Hash().Hex())

	// 等待交易确认
	fmt.Println("等待交易确认...")
	receipt, err := bind.WaitMined(ctx, client, tx)
	if err != nil {
		log.Fatalf("failed to wait for transaction to be mined: %v", err)
	}
	fmt.Printf("交易已确认，区块高度: %d\n", receipt.BlockNumber)

	// 再次查询当前计数
	newCount, err := counter.X(&bind.CallOpts{})
	if err != nil {
		log.Fatalf("failed to call contract method: %v", err)
	}
	fmt.Printf("增加后的计数: %d\n", newCount)

	// 3. 设置自定义计数
	setTx, err := counter.IncBy(auth, big.NewInt(100))
	if err != nil {
		log.Fatalf("failed to send set transaction: %v", err)
	}
	fmt.Printf("设置计数交易: %s\n", setTx.Hash().Hex())

	// 等待交易确认
	_, err = bind.WaitMined(ctx, client, setTx)
	if err != nil {
		log.Fatalf("failed to wait for set transaction to be mined: %v", err)
	}

	// 查询更新后的计数
	finalCount, err := counter.X(&bind.CallOpts{})
	if err != nil {
		log.Fatalf("failed to call contract method: %v", err)
	}
	fmt.Printf("最终计数: %d\n", finalCount)
}

// trim0x 移除十六进制字符串前缀 "0x"
func trim0x(s string) string {
	if len(s) >= 2 && s[:2] == "0x" {
		return s[2:]
	}
	return s
}
