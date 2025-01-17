package raftha

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/vault/api"
	"github.com/hashicorp/vault/helper/testhelpers"
	"github.com/hashicorp/vault/helper/testhelpers/teststorage"
	consulstorage "github.com/hashicorp/vault/helper/testhelpers/teststorage/consul"
	vaulthttp "github.com/hashicorp/vault/http"
	"github.com/hashicorp/vault/physical/raft"
	"github.com/hashicorp/vault/sdk/helper/logging"
	"github.com/hashicorp/vault/vault"
)

func TestRaft_HA_NewCluster(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		t.Parallel()

		t.Run("no_client_certs", func(t *testing.T) {
			testRaftHANewCluster(t, teststorage.MakeFileBackend, false)
		})

		t.Run("with_client_certs", func(t *testing.T) {
			testRaftHANewCluster(t, teststorage.MakeFileBackend, true)
		})
	})

	t.Run("inmem", func(t *testing.T) {
		t.Parallel()

		t.Run("no_client_certs", func(t *testing.T) {
			testRaftHANewCluster(t, teststorage.MakeInmemBackend, false)
		})

		t.Run("with_client_certs", func(t *testing.T) {
			testRaftHANewCluster(t, teststorage.MakeInmemBackend, true)
		})
	})

	t.Run("consul", func(t *testing.T) {
		t.Parallel()

		t.Run("no_client_certs", func(t *testing.T) {
			testRaftHANewCluster(t, consulstorage.MakeConsulBackend, false)
		})

		t.Run("with_client_certs", func(t *testing.T) {
			testRaftHANewCluster(t, consulstorage.MakeConsulBackend, true)
		})
	})
}

func testRaftHANewCluster(t *testing.T, bundler teststorage.PhysicalBackendBundler, addClientCerts bool) {
	var conf vault.CoreConfig
	opts := vault.TestClusterOptions{HandlerFunc: vaulthttp.Handler}

	teststorage.RaftHASetup(&conf, &opts, bundler)
	cluster := vault.NewTestCluster(t, &conf, &opts)
	cluster.Start()
	defer cluster.Cleanup()

	addressProvider := &testhelpers.TestRaftServerAddressProvider{Cluster: cluster}

	leaderCore := cluster.Cores[0]
	atomic.StoreUint32(&vault.TestingUpdateClusterAddr, 1)

	// Seal the leader so we can install an address provider
	{
		testhelpers.EnsureCoreSealed(t, leaderCore)
		leaderCore.UnderlyingHAStorage.(*raft.RaftBackend).SetServerAddressProvider(addressProvider)
		cluster.UnsealCore(t, leaderCore)
		vault.TestWaitActive(t, leaderCore.Core)
	}

	// Now unseal core for join commands to work
	testhelpers.EnsureCoresUnsealed(t, cluster)

	joinFunc := func(client *api.Client, addClientCerts bool) {
		req := &api.RaftJoinRequest{
			LeaderCACert: string(cluster.CACertPEM),
		}
		if addClientCerts {
			req.LeaderClientCert = string(cluster.CACertPEM)
			req.LeaderClientKey = string(cluster.CAKeyPEM)
		}
		resp, err := client.Sys().RaftJoinWithContext(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if !resp.Joined {
			t.Fatalf("failed to join raft cluster")
		}
	}

	joinFunc(cluster.Cores[1].Client, addClientCerts)
	joinFunc(cluster.Cores[2].Client, addClientCerts)

	// Ensure peers are added
	leaderClient := cluster.Cores[0].Client
	testhelpers.VerifyRaftPeers(t, leaderClient, map[string]bool{
		"core-0": true,
		"core-1": true,
		"core-2": true,
	})

	// Test remove peers
	_, err := leaderClient.Logical().WriteWithContext(context.Background(), "sys/storage/raft/remove-peer", map[string]interface{}{
		"server_id": "core-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = leaderClient.Logical().WriteWithContext(context.Background(), "sys/storage/raft/remove-peer", map[string]interface{}{
		"server_id": "core-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Ensure peers are removed
	testhelpers.VerifyRaftPeers(t, leaderClient, map[string]bool{
		"core-0": true,
	})
}

func TestRaft_HA_ExistingCluster(t *testing.T) {
	t.Parallel()
	conf := vault.CoreConfig{
		DisablePerformanceStandby: true,
	}
	opts := vault.TestClusterOptions{
		HandlerFunc:        vaulthttp.Handler,
		NumCores:           vault.DefaultNumCores,
		KeepStandbysSealed: true,
	}
	logger := logging.NewVaultLogger(hclog.Debug).Named(t.Name())

	physBundle := teststorage.MakeInmemBackend(t, logger)
	physBundle.HABackend = nil

	storage, cleanup := teststorage.MakeReusableStorage(t, logger, physBundle)
	defer cleanup()

	var (
		clusterBarrierKeys [][]byte
		clusterRootToken   string
	)
	createCluster := func(t *testing.T) {
		t.Log("simulating cluster creation without raft as HABackend")

		storage.Setup(&conf, &opts)

		cluster := vault.NewTestCluster(t, &conf, &opts)
		cluster.Start()
		defer func() {
			cluster.Cleanup()
			storage.Cleanup(t, cluster)
		}()

		clusterBarrierKeys = cluster.BarrierKeys
		clusterRootToken = cluster.RootToken
	}

	createCluster(t)

	haStorage, haCleanup := teststorage.MakeReusableRaftHAStorage(t, logger, opts.NumCores, physBundle)
	defer haCleanup()

	updateCluster := func(t *testing.T) {
		t.Log("simulating cluster update with raft as HABackend")

		opts.SkipInit = true
		haStorage.Setup(&conf, &opts)

		cluster := vault.NewTestCluster(t, &conf, &opts)
		cluster.Start()
		defer func() {
			cluster.Cleanup()
			haStorage.Cleanup(t, cluster)
		}()

		// Set cluster values
		cluster.BarrierKeys = clusterBarrierKeys
		cluster.RootToken = clusterRootToken

		addressProvider := &testhelpers.TestRaftServerAddressProvider{Cluster: cluster}
		atomic.StoreUint32(&vault.TestingUpdateClusterAddr, 1)

		// Seal the leader so we can install an address provider
		leaderCore := cluster.Cores[0]
		{
			testhelpers.EnsureCoreSealed(t, leaderCore)
			leaderCore.UnderlyingHAStorage.(*raft.RaftBackend).SetServerAddressProvider(addressProvider)
			testhelpers.EnsureCoreUnsealed(t, cluster, leaderCore)
		}

		// Call the bootstrap on the leader and then ensure that it becomes active
		leaderClient := cluster.Cores[0].Client
		leaderClient.SetToken(clusterRootToken)
		{
			_, err := leaderClient.Logical().WriteWithContext(context.Background(), "sys/storage/raft/bootstrap", nil)
			if err != nil {
				t.Fatal(err)
			}
			vault.TestWaitActive(t, leaderCore.Core)
		}

		// Set address provider
		cluster.Cores[1].UnderlyingHAStorage.(*raft.RaftBackend).SetServerAddressProvider(addressProvider)
		cluster.Cores[2].UnderlyingHAStorage.(*raft.RaftBackend).SetServerAddressProvider(addressProvider)

		// Now unseal core for join commands to work
		testhelpers.EnsureCoresUnsealed(t, cluster)

		joinFunc := func(client *api.Client) {
			req := &api.RaftJoinRequest{
				LeaderCACert: string(cluster.CACertPEM),
			}
			resp, err := client.Sys().RaftJoinWithContext(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if !resp.Joined {
				t.Fatalf("failed to join raft cluster")
			}
		}

		joinFunc(cluster.Cores[1].Client)
		joinFunc(cluster.Cores[2].Client)

		// Ensure peers are added
		testhelpers.VerifyRaftPeers(t, leaderClient, map[string]bool{
			"core-0": true,
			"core-1": true,
			"core-2": true,
		})
	}

	updateCluster(t)
}
