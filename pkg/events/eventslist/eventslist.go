/**
 * Copyright 2022 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package eventslist

const (
  Client_TargetAdded                = "Target Added"
  Client_TargetsRemoved             = "Targets Removed"
  Client_TargetsCleared             = "Target Cleared"
  Client_ResultsCleared             = "Results Cleared"
  Client_TargetInvoked              = "Target Invoked"
  Client_TargetsStopped             = "Targets Stopped"
  Client_TrackingHeadersAdded       = "Tracking Headers Added"
  Client_TrackingHeadersCleared     = "Tracking Headers Cleared"
  Client_TrackingTimeBucketAdded    = "Tracking TimeBucket Added"
  Client_TrackingTimeBucketsCleared = "Tracking TimeBuckets Cleared"
  Client_CACertStored               = "Client CA Cert Stored"
  Client_CACertRemoved              = "Client CA Cert Removed"
  Client_InvocationStarted          = "Invocation Started"
  Client_InvocationFinished         = "Invocation Finished"
  Client_InvocationRepeatedResponse = "Invocation Repeated Response"
  Client_InvocationRepeatedFailure  = "Invocation Repeated Failure"
  Client_InvocationResponse         = "Invocation Response"
  Client_InvocationFailure          = "Invocation Failure"

  Jobs_JobAdded          = "Job Added"
  Jobs_JobScriptStored   = "Job Script Stored"
  Jobs_JobFileStored     = "Job File Stored"
  Jobs_JobsRemoved       = "Jobs Removed"
  Jobs_JobsCleared       = "Jobs Cleared"
  Jobs_JobResultsCleared = "Job Results Cleared"
  Jobs_JobStarted        = "Job Started"
  Jobs_JobFinished       = "Job Finished"
  Jobs_JobStopped        = "Job Stopped"

  Registry_PeerEventsCleared              = "Registry: Peer Events Cleared"
  Registry_PeerResultsCleared             = "Registry: Peer Results Cleared"
  Registry_PeerAdded                      = "Registry: Peer Added"
  Registry_PeerRejected                   = "Registry: Peer Rejected"
  Registry_PeerRemoved                    = "Registry: Peer Removed"
  Registry_CheckedPeersHealth             = "Registry: Checked Peers Health"
  Registry_CleanedUpUnhealthyPeers        = "Registry: Cleaned Up Unhealthy Peers"
  Registry_LockerOpened                   = "Registry: Locker Opened"
  Registry_LockerClosed                   = "Registry: Locker Closed"
  Registry_LockerCleared                  = "Registry: Locker Cleared"
  Registry_AllLockersCleared              = "Registry: All Lockers Cleared"
  Registry_LockerDataStored               = "Registry: Locker Data Stored"
  Registry_LockerDataRemoved              = "Registry: Locker Data Removed"
  Registry_PeerInstanceLockerCleared      = "Registry: Peer Instance Locker Cleared"
  Registry_PeerLockerCleared              = "Registry: Peer Locker Cleared"
  Registry_AllPeerLockersCleared          = "Registry: All Peer Lockers Cleared"
  Registry_PeerTargetRejected             = "Registry: Peer Target Rejected"
  Registry_PeerTargetAdded                = "Registry: Peer Target Added"
  Registry_PeerTargetsRemoved             = "Registry: Peer Targets Removed"
  Registry_PeerTargetsStopped             = "Registry: Peer Targets Stopped"
  Registry_PeerTargetsInvoked             = "Registry: Peer Targets Invoked"
  Registry_PeerJobAdded                   = "Registry: Peer Job Added"
  Registry_PeerJobFileAdded               = "Registry: Peer Job File Added"
  Registry_PeerJobRejected                = "Registry: Peer Job Rejected"
  Registry_PeerJobFileRejected            = "Registry: Peer Job Script Rejected"
  Registry_PeerJobsRemoved                = "Registry: Peer Jobs Removed"
  Registry_PeerJobsStopped                = "Registry: Peer Jobs Stopped"
  Registry_PeerJobsInvoked                = "Registry: Peer Jobs Invoked"
  Registry_PeersEpochsCleared             = "Registry: Peers Epochs Cleared"
  Registry_PeersCleared                   = "Registry: Peers Cleared"
  Registry_PeerTargetsCleared             = "Registry: Peer Targets Cleared"
  Registry_PeerJobsCleared                = "Registry: Peer Jobs Cleared"
  Registry_PeerTrackingHeadersAdded       = "Registry: Peer Tracking Headers Added"
  Registry_PeerTrackingHeadersCleared     = "Registry: Peer Tracking Headers Cleared"
  Registry_PeerTrackingTimeBucketsAdded   = "Registry: Peer Tracking Time Buckets Added"
  Registry_PeerTrackingTimeBucketsCleared = "Registry: Peer Tracking Time Buckets Cleared"
  Registry_PeerProbeSet                   = "Registry: Peer Probe Set"
  Registry_PeerProbeStatusSet             = "Registry: Peer Probe Status Set"
  Registry_PeerCalled                     = "Registry: Peer Called"
  Registry_PeersCopied                    = "Registry: Peers Copied"
  Registry_Cloned                         = "Registry: Cloned"
  Registry_LockersDumped                  = "Registry: Lockers Dumped"
  Registry_Dumped                         = "Registry: Dumped"
  Registry_DumpLoaded                     = "Registry: Dump Loaded"
)
