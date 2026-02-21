package agent

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/lancekrogers/agent-inference-ethden-2026/internal/hcs"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/compute"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/da"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/inft"
	"github.com/lancekrogers/agent-inference-ethden-2026/internal/zerog/storage"
)

// Config holds all configuration for the inference agent.
type Config struct {
	AgentID        string
	DaemonAddr     string
	HealthInterval time.Duration
	Compute        compute.BrokerConfig
	Storage        storage.ClientConfig
	INFT           inft.MinterConfig
	DA             da.PublisherConfig
	HCSTaskTopic   string
	HCSResultTopic string
}

// HCSHandler builds an HCS handler config from the agent config.
func (c *Config) HCSHandler(transport hcs.Transport) hcs.HandlerConfig {
	return hcs.HandlerConfig{
		Transport:     transport,
		TaskTopicID:   c.HCSTaskTopic,
		ResultTopicID: c.HCSResultTopic,
		AgentID:       c.AgentID,
	}
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{}

	cfg.AgentID = os.Getenv("INFERENCE_AGENT_ID")
	if cfg.AgentID == "" {
		return nil, fmt.Errorf("config: INFERENCE_AGENT_ID is required")
	}

	cfg.DaemonAddr = envOr("INFERENCE_DAEMON_ADDR", "localhost:9090")

	healthStr := os.Getenv("INFERENCE_HEALTH_INTERVAL")
	if healthStr == "" {
		cfg.HealthInterval = 30 * time.Second
	} else {
		dur, err := time.ParseDuration(healthStr)
		if err != nil {
			return nil, fmt.Errorf("config: invalid INFERENCE_HEALTH_INTERVAL: %w", err)
		}
		cfg.HealthInterval = dur
	}

	chainRPC := envOr("ZG_CHAIN_RPC", "https://evmrpc-testnet.0g.ai")
	chainPrivKey := os.Getenv("ZG_CHAIN_PRIVATE_KEY")
	var chainID int64 = 16602

	// 0G Compute
	cfg.Compute.ChainRPC = chainRPC
	cfg.Compute.ChainID = chainID
	cfg.Compute.PrivateKey = chainPrivKey
	cfg.Compute.ServingContractAddress = os.Getenv("ZG_SERVING_CONTRACT")
	cfg.Compute.Endpoint = os.Getenv("ZG_COMPUTE_ENDPOINT")
	cfg.Compute.PollInterval = 2 * time.Second
	cfg.Compute.PollTimeout = 5 * time.Minute

	// 0G Storage
	cfg.Storage.ChainRPC = chainRPC
	cfg.Storage.ChainID = chainID
	cfg.Storage.PrivateKey = chainPrivKey
	cfg.Storage.FlowContractAddress = envOr("ZG_FLOW_CONTRACT", "0x22E03a6A89B950F1c82ec5e74F8eCa321a105296")
	cfg.Storage.StorageNodeEndpoint = os.Getenv("ZG_STORAGE_NODE_ENDPOINT")
	cfg.Storage.Endpoint = os.Getenv("ZG_STORAGE_ENDPOINT")

	// 0G iNFT
	cfg.INFT.ChainRPC = chainRPC
	cfg.INFT.ChainID = chainID
	cfg.INFT.ContractAddress = os.Getenv("ZG_INFT_CONTRACT")
	cfg.INFT.PrivateKey = chainPrivKey
	cfg.INFT.EncryptionKeyID = envOr("ZG_ENCRYPTION_KEY_ID", "default")

	encKeyHex := os.Getenv("ZG_ENCRYPTION_KEY")
	if encKeyHex != "" {
		key, err := hex.DecodeString(encKeyHex)
		if err != nil {
			return nil, fmt.Errorf("config: invalid ZG_ENCRYPTION_KEY hex: %w", err)
		}
		cfg.INFT.EncryptionKey = key
	}

	// 0G DA
	cfg.DA.ChainRPC = chainRPC
	cfg.DA.ChainID = chainID
	cfg.DA.PrivateKey = chainPrivKey
	cfg.DA.DAContractAddress = envOr("ZG_DA_CONTRACT", "0xE75A073dA5bb7b0eC622170Fd268f35E675a957B")
	cfg.DA.Namespace = envOr("ZG_DA_NAMESPACE", "inference-audit")
	cfg.DA.Endpoint = os.Getenv("ZG_DA_ENDPOINT")

	// HCS
	cfg.HCSTaskTopic = os.Getenv("HCS_TASK_TOPIC")
	cfg.HCSResultTopic = os.Getenv("HCS_RESULT_TOPIC")

	return cfg, nil
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
