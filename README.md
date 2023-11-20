<!--
SPDX-License-Identifier: Apache-2.0
-->

# PFCP-LB
This project is based on [OMEC UPF](https://github.com/omec-project/upf),
One of the most important elements of Virtual-UPF is PFCP-LB which contains some Kubernetes resources like Service, Pod, ConfigMap, ServiceAccount and RoleBinding. Its Pod contains a container that runs an application implemented in Golang.

This intermediate Pod is introduced to SMF as a single UPF and also is introduced to UPFs of Virtual-UPF as the SMF. LB-PFCP has 2 PFCP-Agents (upPFCP-Agent and downPFCP-Agent) which are handled by different CPU cores to increase the performance. Each PFCP-Agent contains a PFCP-Node which handles the PFCP-Connections toward UPFs for downPFCP-Agent and toward SMF for upPFCP-Agent. So, the downPFCP-Agent is for connecting the PFCP-LB to UPFs, and upPFCP-Agent is used to connect PFCP-LB to SMF. In other words, downPFCP-Agent is SMF of UPFs and upPFCP-Agent is the entity that SMF sees as its UPF.

These two PFCP-Agents, which are run in two different Go routines, communicate with each other through a set of Go channels. These channels are used to pass PFCP messages that upPFCP-Agents received from SMF and want to forward it to UPFs. So downPFCP-Agent is always listening to these channels and once it receives any message from each channel, does the required process and forward it toward the appropriate UPF through an already created PFCP-Connection. When the UPF sends the response to downPFCP-Agent, downPFCP-Agent does the required process and sends the result to upPFCP-Agent and upPFCP-Agent creates a new response based on received response and send it to SMF.

DownPFCP-Agent also contains an HTTP server which is used to register UPFs. UPFs on startup sends some data to the downPFCP-Agent which is used to create a PFCP-Connection between PFCP-LB and UPFs.

### How does it work ?
The main work flow of Virtual-UPF can be divided into two main phases. INIT phase and action phase. The purpose of the INIT phase is to run the Virtual-UPF and all of its components, do the initial configuration and make everything ready for the action phase. In the Action Phase the Virtual-UPF is totally ready. So, it starts to handle the data plane traffic of user equipments. Below I break down these two phases and explained everything with sufficient details.

### Supported PFCP messages
* PFCP-Session Establishment Request/Responce
* PFCP-Session Modification Request/Responce
* PFCP-Session Deletion Request/Responce

## Features
### Auto Scale-out
If the Auto Scale-out feature is enabled, the PFCP-LB continuously checks the state of UPFs. if the number of sessions of each UPFs reaches to a certain number, and the current number of active UPFs are less than configured MaxUPFs, the Auto Scale-out procedure will be triggered. This number of sessions is calculated like:
* MaxThreshold + (MaxThreshold*MaxTolerance)

The PFCP-LB has the permission to deploy a UPF because of its RBAC, so find the first UPF which is not deployed yet, and add it to the virtual UPF. When a new UPF starts up, as we explained before, it registers itself in West-LB, East-LB, and PFCP-LB. So, West-LB and East-LB do the regular thing that they normally do with a new UPF. When PFCP-LB receives the registration request, creates a PFCP-Connection with it and adds it to the list of its registered UPFs. Now there is a new UPF in the Virtual-UPF which does not handle any session. 

So PFCP-LB who knows each UPF currently is handling how many sessions, calls the MakeUPFsLighter function. 
### Auto Scale-in
Like Auto Scale-out, if Auto Scale-in feature is enabled, the PFCP-LB continuously checks the state of UPFs. If the number of sessions of each UPFs becomes below a certain number, and the current number of active UPFs are more than configured MinUPFs, the Auto Scale-in procedure will be triggered. This number of sessions is calculated like:

* MinThreshold - (MinThreshold*MinTolerance)

When this procedure is triggered, the lightest UPF is selected to be eliminated. But this UPF is still handling some sessions which should be transferred to another UPF before eliminating the lightest UPF. So, the MakeUPFEmpty function will be called for the UPF that causes the Scale-in process to be triggered. 

### Live Session Migration
One of the most important features of Virtual-UPF that makes the Scale-in and Scale-out procedure seamless, is its Live Session Migration that is done by the TransferSessions function.

## Configuration Variables

### MinUPFs
The initial number of UPFs that is deployed when the Virtual-UPF starts, and also it is the minimum number of UPFs that after the Scale-in process we can have inside the Virtual-UPF. Its value will be equal to 2 in case of leaving it blank in configuration to archive the redundancy.
###	MaxUPFs
Maximum number of UPFs that we are allowed to have after Scale-out procedure.
###	MaxThreshold
The maximum number of sessions that if a UPF handles, we consider that UPF as a normal UPF. Each UPF is allowed to handle sessions more than this threshold, but the sessions after the threshold is known as excess sessions that should be transferred to a lighter UPF.
###	MaxTolerance
It is a percentage on MaxThreshold that indicates when the Auto Scale-out should be triggered and is useful to make the Auto Scale-out tolerate against the small changes in the numbers of sessions. 

Let’s make it more clear with an example. Assume the MaxThreshold is equal to 10.000 and MaxTolerance is equal to %10. If the number of sessions handled by any UPF reaches to 11.000, the auto scale of procedure will be triggered and another UPF will be added to the Virtual-UPF and its excess session will be transferred to the new UPF.
###	AutoScaleOut
It is a Boolean variable which turns the Auto Scale-out process on or off. If this feature is turned off, the deployment of the new UPF should be done manually.
###	Other Variables
AutoScaleIn, MinThreshold and MinTolerance have similar concepts to previous features, except they are used for the Auto Scale-in procedure.

## Create docker image



To build all Docker images run:

```
make docker-build
```

To build a selected image use `DOCKER_TARGETS`:

```
DOCKER_TARGETS=pfcpiface make docker-build
```
