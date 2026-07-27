// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/cosi-project/runtime/pkg/state/impl/inmem"
	"github.com/cosi-project/runtime/pkg/state/impl/namespaced"
	"github.com/siderolabs/omni/client/api/omni/specs"
	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// hookedState wraps a core state so that tests can intercept watch establishment and reads.
type hookedState struct {
	state.CoreState

	onWatchKind func(ctx context.Context, kind resource.Kind) error
	onGet       func(ctx context.Context, ptr resource.Pointer) error
}

func (s *hookedState) WatchKind(ctx context.Context, kind resource.Kind, ch chan<- state.Event, opts ...state.WatchKindOption) error {
	if s.onWatchKind != nil {
		if err := s.onWatchKind(ctx, kind); err != nil {
			return err
		}
	}

	return s.CoreState.WatchKind(ctx, kind, ch, opts...)
}

func (s *hookedState) Get(ctx context.Context, ptr resource.Pointer, opts ...state.GetOption) (resource.Resource, error) {
	if s.onGet != nil {
		if err := s.onGet(ctx, ptr); err != nil {
			return nil, err
		}
	}

	return s.CoreState.Get(ctx, ptr, opts...)
}

// newTestCoreState builds an in-memory core state populated with a fixed set of Omni resources.
func newTestCoreState(ctx context.Context, t *testing.T) state.CoreState {
	t.Helper()

	coreState := namespaced.NewState(inmem.Build)
	st := state.WrapCore(coreState)

	clusterStatus := omni.NewClusterStatus("talos-default")
	clusterStatus.TypedSpec().Value.Phase = specs.ClusterStatusSpec_RUNNING
	clusterStatus.TypedSpec().Value.Ready = true
	clusterStatus.TypedSpec().Value.Available = true
	clusterStatus.TypedSpec().Value.KubernetesAPIReady = true
	clusterStatus.TypedSpec().Value.ControlplaneReady = true
	clusterStatus.TypedSpec().Value.TalosVersion = "1.11.2"
	clusterStatus.TypedSpec().Value.KubernetesVersion = "1.34.1"
	clusterStatus.TypedSpec().Value.Machines = &specs.Machines{
		Total:     2,
		Healthy:   1,
		Connected: 2,
		Requested: 3,
	}
	require.NoError(t, st.Create(ctx, clusterStatus))

	machineStatus1 := omni.NewMachineStatus("machine-1")
	machineStatus1.TypedSpec().Value.Connected = true
	machineStatus1.TypedSpec().Value.PowerState = specs.MachineStatusSpec_POWER_STATE_ON
	machineStatus1.TypedSpec().Value.Cluster = "talos-default"
	machineStatus1.TypedSpec().Value.Role = specs.MachineStatusSpec_CONTROL_PLANE
	machineStatus1.TypedSpec().Value.TalosVersion = "1.11.2"
	machineStatus1.TypedSpec().Value.ManagementAddress = "10.5.0.2"
	machineStatus1.TypedSpec().Value.Network = &specs.MachineStatusSpec_NetworkStatus{Hostname: "node-1"}
	machineStatus1.TypedSpec().Value.PlatformMetadata = &specs.MachineStatusSpec_PlatformMetadata{Platform: "metal"}
	machineStatus1.TypedSpec().Value.Hardware = &specs.MachineStatusSpec_HardwareStatus{Arch: "amd64"}
	require.NoError(t, st.Create(ctx, machineStatus1))

	// an unallocated machine with no network status, falling back to the platform metadata hostname
	machineStatus2 := omni.NewMachineStatus("machine-2")
	machineStatus2.TypedSpec().Value.PlatformMetadata = &specs.MachineStatusSpec_PlatformMetadata{
		Platform: "aws",
		Hostname: "platform-hostname",
	}
	require.NoError(t, st.Create(ctx, machineStatus2))

	clusterMachineStatus := omni.NewClusterMachineStatus("machine-1")
	clusterMachineStatus.Metadata().Labels().Set(omni.LabelCluster, "talos-default")
	clusterMachineStatus.Metadata().Labels().Set(omni.LabelMachineSet, "talos-default-control-planes")
	clusterMachineStatus.TypedSpec().Value.Stage = specs.ClusterMachineStatusSpec_RUNNING
	clusterMachineStatus.TypedSpec().Value.Ready = true
	require.NoError(t, st.Create(ctx, clusterMachineStatus))

	machineSetStatus := omni.NewMachineSetStatus("talos-default-control-planes")
	machineSetStatus.Metadata().Labels().Set(omni.LabelCluster, "talos-default")
	machineSetStatus.TypedSpec().Value.Phase = specs.MachineSetPhase_Running
	machineSetStatus.TypedSpec().Value.Ready = true
	machineSetStatus.TypedSpec().Value.Machines = &specs.Machines{
		Total:     1,
		Healthy:   1,
		Connected: 1,
		Requested: 1,
	}
	require.NoError(t, st.Create(ctx, machineSetStatus))

	talosUpgradeStatus := omni.NewTalosUpgradeStatus("talos-default")
	talosUpgradeStatus.TypedSpec().Value.Phase = specs.TalosUpgradeStatusSpec_Done
	require.NoError(t, st.Create(ctx, talosUpgradeStatus))

	kubernetesUpgradeStatus := omni.NewKubernetesUpgradeStatus("talos-default")
	kubernetesUpgradeStatus.TypedSpec().Value.Phase = specs.KubernetesUpgradeStatusSpec_Upgrading
	require.NoError(t, st.Create(ctx, kubernetesUpgradeStatus))

	cluster := omni.NewCluster("talos-default")
	cluster.TypedSpec().Value.BackupConfiguration = &specs.EtcdBackupConf{
		Enabled:  true,
		Interval: nil,
	}
	require.NoError(t, st.Create(ctx, cluster))

	etcdBackupStatus := omni.NewEtcdBackupStatus("talos-default")
	etcdBackupStatus.TypedSpec().Value.Status = specs.EtcdBackupStatusSpec_Ok
	etcdBackupStatus.TypedSpec().Value.LastBackupTime = timestamppb.New(time.Unix(1751500000, 0))
	etcdBackupStatus.TypedSpec().Value.LastBackupAttempt = timestamppb.New(time.Unix(1751500060, 0))
	require.NoError(t, st.Create(ctx, etcdBackupStatus))

	// backups enabled, but none attempted yet: no backup status resource exists
	pendingCluster := omni.NewCluster("backup-pending")
	pendingCluster.TypedSpec().Value.BackupConfiguration = &specs.EtcdBackupConf{Enabled: true}
	require.NoError(t, st.Create(ctx, pendingCluster))

	// backups not configured at all
	disabledCluster := omni.NewCluster("backup-disabled")
	require.NoError(t, st.Create(ctx, disabledCluster))

	etcdBackupOverallStatus := omni.NewEtcdBackupOverallStatus()
	etcdBackupOverallStatus.TypedSpec().Value.ConfigurationName = "s3"
	require.NoError(t, st.Create(ctx, etcdBackupOverallStatus))

	return coreState
}
