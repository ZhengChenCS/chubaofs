// Copyright 2018 The Chubao Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package cmd

import (
	"fmt"
	"github.com/chubaofs/chubaofs/proto"
	"github.com/chubaofs/chubaofs/sdk/master"
	"github.com/spf13/cobra"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cmdMetaPartitionUse   = "metapartition [COMMAND]"
	cmdMetaPartitionShort = "Manage meta partition"
)

func newMetaPartitionCmd(client *master.MasterClient) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   cmdMetaPartitionUse,
		Short: cmdMetaPartitionShort,
	}
	cmd.AddCommand(
		newMetaPartitionGetCmd(client),
		newListCorruptMetaPartitionCmd(client),
		newMetaPartitionDecommissionCmd(client),
		newMetaPartitionReplicateCmd(client),
		newMetaPartitionDeleteReplicaCmd(client),
	)
	return cmd
}

const (
	cmdMetaPartitionGetShort           = "Display detail information of a meta partition"
	cmdCheckCorruptMetaPartitionShort  = "Check out corrupt meta partitions"
	cmdMetaPartitionDecommissionShort  = "Decommission a replication of the meta partition to a new address"
	cmdMetaPartitionReplicateShort     = "Add a replication of the meta partition on a new address"
	cmdMetaPartitionDeleteReplicaShort = "Delete a replication of the meta partition on a fixed address"
)

func newMetaPartitionGetCmd(client *master.MasterClient) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   CliOpInfo + " [META PARTITION ID]",
		Short: cmdMetaPartitionGetShort,
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			var (
				partition *proto.MetaPartitionInfo
			)
			partitionID, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return
			}
			if partition, err = client.ClientAPI().GetMetaPartition(partitionID); err != nil {
				return
			}
			stdout(formatMetaPartitionInfo(partition))
		},
	}
	return cmd
}

func newListCorruptMetaPartitionCmd(client *master.MasterClient) *cobra.Command {
	var optCheckAll bool
	var cmd = &cobra.Command{
		Use:   CliOpCheck,
		Short: cmdCheckCorruptMetaPartitionShort,
		Long: `If the meta nodes are marked as "Inactive", it means the nodes has been not available for a long time. It is suggested to eliminate
the network, disk or other problems first. If the bad nodes can never be "active" again, they are called corrupt nodes. And the 
"decommission" command can be used to discard the corrupt nodes. However, if more than half replicas of a partition are on 
the corrupt nodes, the few remaining replicas can not reach an agreement with one leader. In this case, you can use the 
"metapartition reset" command to fix the problem, however this action may lead to data loss, be careful to do this. The 
"reset" command will be released in next version.`,
		Run: func(cmd *cobra.Command, args []string) {
			var (
				diagnosis *proto.MetaPartitionDiagnosis
				metaNodes []*proto.MetaNodeInfo
				err       error
			)
			if optCheckAll {
				err = checkAllMetaPartitions(client)
				if err != nil {
					stdout("%v\n", err)
				}
				return
			}
			if diagnosis, err = client.AdminAPI().DiagnoseMetaPartition(); err != nil {
				stdout("%v\n", err)
			}
			stdout("[Inactive Meta nodes]:\n")
			stdout("%v\n", formatMetaNodeDetailTableHeader())
			sort.SliceStable(diagnosis.InactiveMetaNodes, func(i, j int) bool {
				return diagnosis.InactiveMetaNodes[i] < diagnosis.InactiveMetaNodes[j]
			})
			for _, addr := range diagnosis.InactiveMetaNodes {
				var node *proto.MetaNodeInfo
				node, err = client.NodeAPI().GetMetaNode(addr)
				metaNodes = append(metaNodes, node)
			}
			sort.SliceStable(metaNodes, func(i, j int) bool {
				return metaNodes[i].ID < metaNodes[j].ID
			})
			for _, node := range metaNodes {
				stdout("%v\n", formatMetaNodeDetail(node, true))
			}

			stdout("\n")
			stdout("[Corrupt meta partitions](no leader):\n")
			stdout("%v\n", partitionInfoTableHeader)
			sort.SliceStable(diagnosis.CorruptMetaPartitionIDs, func(i, j int) bool {
				return diagnosis.CorruptMetaPartitionIDs[i] < diagnosis.CorruptMetaPartitionIDs[j]
			})
			for _, pid := range diagnosis.CorruptMetaPartitionIDs {
				var partition *proto.MetaPartitionInfo
				if partition, err = client.ClientAPI().GetMetaPartition(pid); err != nil {
					stdout("Partition not found, err:[%v]", err)
					return
				}
				stdout("%v\n", formatMetaPartitionInfoRow(partition))
			}

			stdout("\n")
			stdout("%v\n", "[Partition lack replicas]:")
			stdout("%v\n", partitionInfoTableHeader)
			sort.SliceStable(diagnosis.LackReplicaMetaPartitionIDs, func(i, j int) bool {
				return diagnosis.LackReplicaMetaPartitionIDs[i] < diagnosis.LackReplicaMetaPartitionIDs[j]
			})
			for _, pid := range diagnosis.LackReplicaMetaPartitionIDs {
				var partition *proto.MetaPartitionInfo
				if partition, err = client.ClientAPI().GetMetaPartition(pid); err != nil {
					stdout("Partition not found, err:[%v]", err)
					return
				}
				if partition != nil {
					stdout("%v\n", formatMetaPartitionInfoRow(partition))
					sort.Strings(partition.Hosts)
					for _, r := range partition.Replicas {
						var mnPartition *proto.MNMetaPartitionInfo
						var err error
						addr := strings.Split(r.Addr, ":")[0]
						if mnPartition, err = client.NodeAPI().MetaNodeGetPartition(addr, partition.PartitionID); err != nil {
							fmt.Printf(partitionInfoColorTablePattern+"\n",
								"", "", "", r.Addr, fmt.Sprintf("%v/%v", 0, partition.ReplicaNum), "no data")
							continue
						}
						mnHosts := make([]string, 0)
						for _, peer := range mnPartition.Peers {
							mnHosts = append(mnHosts, peer.Addr)
						}
						sort.Strings(mnHosts)
						fmt.Printf(partitionInfoColorTablePattern+"\n",
							"", "", "", r.Addr, fmt.Sprintf("%v/%v", len(mnPartition.Peers), partition.ReplicaNum), strings.Join(mnHosts, "; "))
					}
					fmt.Printf("\033[1;40;32m%-8v\033[0m", strings.Repeat("_ ", len(partitionInfoTableHeader)/2+5)+"\n")
				}
			}
			return
		},
	}
	cmd.Flags().BoolVar(&optCheckAll, "all", false, "true - check all partitions; false - only check partitions which lack of replica")
	return cmd
}
func checkAllMetaPartitions(client *master.MasterClient) (err error) {
	var volInfo []*proto.VolInfo
	if volInfo, err = client.AdminAPI().ListVols(""); err != nil {
		stdout("%v\n", err)
		return
	}
	stdout("\n")
	stdout("%v\n", "[Partition peer info not valid]:")
	stdout("%v\n", partitionInfoTableHeader)
	for _, vol := range volInfo {
		var volView *proto.VolView
		if volView, err = client.ClientAPI().GetVolume(vol.Name, calcAuthKey(vol.Owner)); err != nil {
			stdout("Found an invalid vol: %v\n", vol.Name)
			continue
		}
		sort.SliceStable(volView.MetaPartitions, func(i, j int) bool {
			return volView.MetaPartitions[i].PartitionID < volView.MetaPartitions[j].PartitionID
		})
		var wg sync.WaitGroup
		for _, mp := range volView.MetaPartitions {
			wg.Add(1)
			go func(mp *proto.MetaPartitionView) {
				defer wg.Done()
				var outPut string
				var isHealthy bool
				outPut, isHealthy, _ = checkMetaPartition(mp.PartitionID, client)
				if !isHealthy {
					fmt.Printf(outPut)
					stdoutGreen(strings.Repeat("_ ", len(partitionInfoTableHeader)/2+20) + "\n")
				}
				time.Sleep(time.Millisecond * 10)
			}(mp)
		}
		wg.Wait()
	}
	return
}
func checkMetaPartition(pid uint64, client *master.MasterClient) (outPut string, isHealthy bool, err error) {
	var partition *proto.MetaPartitionInfo
	var sb = strings.Builder{}
	isHealthy = true
	if partition, err = client.ClientAPI().GetMetaPartition(pid); err != nil {
		sb.WriteString(fmt.Sprintf("Partition is not found, err:[%v]", err))
		return
	}
	if partition != nil {
		sb.WriteString(fmt.Sprintf("%v\n", formatMetaPartitionInfoRow(partition)))
		sort.Strings(partition.Hosts)
		if len(partition.MissNodes) > 0 || partition.Status == -1 || len(partition.Hosts) != int(partition.ReplicaNum) {
			errMsg := fmt.Sprintf("The partition is unhealthy according to the report message from master")
			sb.WriteString(fmt.Sprintf("\033[1;40;31m%-8v\033[0m\n", errMsg))
			isHealthy = false
		}
		for _, r := range partition.Replicas {
			var mnPartition *proto.MNMetaPartitionInfo
			var err error
			addr := strings.Split(r.Addr, ":")[0]
			if mnPartition, err = client.NodeAPI().MetaNodeGetPartition(addr, partition.PartitionID); err != nil {
				sb.WriteString(fmt.Sprintf(partitionInfoColorTablePattern+"\n",
					"", "", "", fmt.Sprintf("%v", r.Addr), fmt.Sprintf("%v/%v", "nil", partition.ReplicaNum), fmt.Sprintf("get partition info failed, err:%v", err)))
				isHealthy = false
				continue
			}

			peerStrings := convertPeersToArray(mnPartition.Peers)
			sort.Strings(peerStrings)
			sb.WriteString(fmt.Sprintf(partitionInfoColorTablePattern+"\n",
				"", "", "", fmt.Sprintf("%v(peers)", r.Addr), fmt.Sprintf("%v/%v", len(peerStrings), partition.ReplicaNum), strings.Join(peerStrings, "; ")))
			if !isEqualStrings(partition.Hosts, peerStrings) {
				isHealthy = false
			}
			if len(peerStrings) != int(partition.ReplicaNum) {
				isHealthy = false
			}
		}
	}
	outPut = sb.String()
	return
}
func newMetaPartitionDecommissionCmd(client *master.MasterClient) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   CliOpDecommission + " [ADDRESS] [META PARTITION ID]",
		Short: cmdMetaPartitionDecommissionShort,
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			address := args[0]
			partitionID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				stdout("%v\n", err)
				return
			}
			if err = client.AdminAPI().DecommissionMetaPartition(partitionID, address); err != nil {
				stdout("%v\n", err)
				return
			}
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return validMetaNodes(client, toComplete), cobra.ShellCompDirectiveNoFileComp
		},
	}
	return cmd
}

func newMetaPartitionReplicateCmd(client *master.MasterClient) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   CliOpReplicate + " [ADDRESS] [META PARTITION ID]",
		Short: cmdMetaPartitionReplicateShort,
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			address := args[0]
			partitionID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				stdout("%v\n", err)
				return
			}
			if err = client.AdminAPI().AddMetaReplica(partitionID, address); err != nil {
				stdout("%v\n", err)
				return
			}
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return validMetaNodes(client, toComplete), cobra.ShellCompDirectiveNoFileComp
		},
	}
	return cmd
}

func newMetaPartitionDeleteReplicaCmd(client *master.MasterClient) *cobra.Command {
	var cmd = &cobra.Command{
		Use:   CliOpDelReplica + " [ADDRESS] [META PARTITION ID]",
		Short: cmdMetaPartitionDeleteReplicaShort,
		Args:  cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			address := args[0]
			partitionID, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				stdout("%v\n", err)
				return
			}
			if err = client.AdminAPI().DeleteMetaReplica(partitionID, address); err != nil {
				stdout("%v\n", err)
				return
			}
		},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return validMetaNodes(client, toComplete), cobra.ShellCompDirectiveNoFileComp
		},
	}
	return cmd
}
