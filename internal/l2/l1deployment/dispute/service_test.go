package dispute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethera-labs/local-testnet/configs"
	"github.com/ethereum/go-ethereum/common"
)

func TestParseDeploymentContractsFromConfigJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := `{
  "l1": {
    "deployed": {
      "disputeGameFactory": "0x2222222222222222222222222222222222222222"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	svc := &Service{
		contractsDir: dir,
		cfg:          configs.L2{},
	}

	got, err := svc.parseDeploymentContracts()
	if err != nil {
		t.Fatalf("expected parse to succeed, got: %v", err)
	}

	if got.DisputeGameFactoryAddress != common.HexToAddress("0x2222222222222222222222222222222222222222") {
		t.Fatalf("unexpected dispute game factory address: %s", got.DisputeGameFactoryAddress)
	}
}

func TestParseDeploymentContractsMissingAddress(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := `{"l1": {"deployed": {}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	svc := &Service{
		contractsDir: dir,
		cfg:          configs.L2{},
	}

	if _, err := svc.parseDeploymentContracts(); err == nil {
		t.Fatal("expected an error when disputeGameFactory is missing")
	}
}

func TestWriteComposeConfigPreservesRollupsAndDeployed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := `{
  "l1": {
    "guardian": "0x0000000000000000000000000000000000000000",
    "deployed": {
      "disputeGameFactory": "0x2222222222222222222222222222222222222222"
    }
  },
  "rollups": {
    "rollupA": {"chainId": "1"}
  }
}`
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(data), 0644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	svc := &Service{
		contractsDir: dir,
		cfg: configs.L2{
			Dispute: configs.DisputeConfig{
				GuardianAddress: "0x1111111111111111111111111111111111111111",
				OwnerAddress:    "0x1111111111111111111111111111111111111111",
			},
		},
	}

	if err := svc.writeComposeConfig(); err != nil {
		t.Fatalf("writeComposeConfig failed: %v", err)
	}

	out, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read updated config.json: %v", err)
	}

	var doc struct {
		L1 struct {
			Guardian string          `json:"guardian"`
			Deployed json.RawMessage `json:"deployed"`
		} `json:"l1"`
		Rollups json.RawMessage `json:"rollups"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("failed to parse updated config.json: %v", err)
	}

	if doc.L1.Guardian != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("guardian not updated: %s", doc.L1.Guardian)
	}
	if string(doc.L1.Deployed) == "" {
		t.Fatal("deployed section was dropped")
	}
	if string(doc.Rollups) == "" {
		t.Fatal("rollups section was dropped")
	}
}
