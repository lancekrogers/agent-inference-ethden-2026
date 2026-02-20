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
	// AgentID is the unique identifier for this agent instance.
	AgentID string

	// DaemonAddr is the address of the daemon client for registration.
	DaemonAddr string

	// HealthInterval is how often to send heartbeat messages.
	HealthInterval time.Duration

	// Compute holds 0G Compute broker configuration.
	Compute compute.BrokerConfig

	// Storage holds 0G Storage client configuration.
	Storage storage.ClientConfig

	// INFT holds 0G Chain iNFT minter configuration.
	INFT inft.MinterConfig

	// DA holds 0G DA publisher configuration.
	DA da.PublisherConfig

	// HCSTaskTopic is the HCS topic for task assignments.
	HCSTaskTopic string

	// HCSResultTopic is the HCS topic for results.
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

	// 0G Compute
	cfg.Compute.Endpoint = os.Getenv("ZG_COMPUTE_ENDPOINT")
	cfg.Compute.PollInterval = 2 * time.Second
	cfg.Compute.PollTimeout = 5 * time.Minute

	// 0G Storage
	cfg.Storage.Endpoint = os.Getenv("ZG_STORAGE_ENDPOINT")

	// 0G iNFT
	cfg.INFT.ChainRPC = envOr("ZG_CHAIN_RPC", "https://evmrpc-testnet.0g.ai")
	cfg.INFT.ChainID = 16602
	cfg.INFT.ContractAddress = os.Getenv("ZG_INFT_CONTRACT")
	cfg.INFT.PrivateKey = os.Getenv("ZG_CHAIN_PRIVATE_KEY")
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
	cfg.DA.Endpoint = os.Getenv("ZG_DA_ENDPOINT")
	cfg.DA.Namespace = envOr("ZG_DA_NAMESPACE", "inference-audit")

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
