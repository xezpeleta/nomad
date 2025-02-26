package structs

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorhill/cronexpr"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/nomad/helper/args"
	"github.com/mitchellh/copystructure"
	"github.com/ugorji/go/codec"

	hcodec "github.com/hashicorp/go-msgpack/codec"
)

var (
	ErrNoLeader     = fmt.Errorf("No cluster leader")
	ErrNoRegionPath = fmt.Errorf("No path to region")
)

type MessageType uint8

const (
	NodeRegisterRequestType MessageType = iota
	NodeDeregisterRequestType
	NodeUpdateStatusRequestType
	NodeUpdateDrainRequestType
	JobRegisterRequestType
	JobDeregisterRequestType
	EvalUpdateRequestType
	EvalDeleteRequestType
	AllocUpdateRequestType
	AllocClientUpdateRequestType
)

const (
	// IgnoreUnknownTypeFlag is set along with a MessageType
	// to indicate that the message type can be safely ignored
	// if it is not recognized. This is for future proofing, so
	// that new commands can be added in a way that won't cause
	// old servers to crash when the FSM attempts to process them.
	IgnoreUnknownTypeFlag MessageType = 128
)

// RPCInfo is used to describe common information about query
type RPCInfo interface {
	RequestRegion() string
	IsRead() bool
	AllowStaleRead() bool
}

// QueryOptions is used to specify various flags for read queries
type QueryOptions struct {
	// The target region for this query
	Region string

	// If set, wait until query exceeds given index. Must be provided
	// with MaxQueryTime.
	MinQueryIndex uint64

	// Provided with MinQueryIndex to wait for change.
	MaxQueryTime time.Duration

	// If set, any follower can service the request. Results
	// may be arbitrarily stale.
	AllowStale bool

	// If set, used as prefix for resource list searches
	Prefix string
}

func (q QueryOptions) RequestRegion() string {
	return q.Region
}

// QueryOption only applies to reads, so always true
func (q QueryOptions) IsRead() bool {
	return true
}

func (q QueryOptions) AllowStaleRead() bool {
	return q.AllowStale
}

type WriteRequest struct {
	// The target region for this write
	Region string
}

func (w WriteRequest) RequestRegion() string {
	// The target region for this request
	return w.Region
}

// WriteRequest only applies to writes, always false
func (w WriteRequest) IsRead() bool {
	return false
}

func (w WriteRequest) AllowStaleRead() bool {
	return false
}

// QueryMeta allows a query response to include potentially
// useful metadata about a query
type QueryMeta struct {
	// This is the index associated with the read
	Index uint64

	// If AllowStale is used, this is time elapsed since
	// last contact between the follower and leader. This
	// can be used to gauge staleness.
	LastContact time.Duration

	// Used to indicate if there is a known leader node
	KnownLeader bool
}

// WriteMeta allows a write response to include potentially
// useful metadata about the write
type WriteMeta struct {
	// This is the index associated with the write
	Index uint64
}

// NodeRegisterRequest is used for Node.Register endpoint
// to register a node as being a schedulable entity.
type NodeRegisterRequest struct {
	Node *Node
	WriteRequest
}

// NodeDeregisterRequest is used for Node.Deregister endpoint
// to deregister a node as being a schedulable entity.
type NodeDeregisterRequest struct {
	NodeID string
	WriteRequest
}

// NodeUpdateStatusRequest is used for Node.UpdateStatus endpoint
// to update the status of a node.
type NodeUpdateStatusRequest struct {
	NodeID string
	Status string
	WriteRequest
}

// NodeUpdateDrainRequest is used for updatin the drain status
type NodeUpdateDrainRequest struct {
	NodeID string
	Drain  bool
	WriteRequest
}

// NodeEvaluateRequest is used to re-evaluate the ndoe
type NodeEvaluateRequest struct {
	NodeID string
	WriteRequest
}

// NodeSpecificRequest is used when we just need to specify a target node
type NodeSpecificRequest struct {
	NodeID string
	QueryOptions
}

// JobRegisterRequest is used for Job.Register endpoint
// to register a job as being a schedulable entity.
type JobRegisterRequest struct {
	Job *Job
	WriteRequest
}

// JobDeregisterRequest is used for Job.Deregister endpoint
// to deregister a job as being a schedulable entity.
type JobDeregisterRequest struct {
	JobID string
	WriteRequest
}

// JobEvaluateRequest is used when we just need to re-evaluate a target job
type JobEvaluateRequest struct {
	JobID string
	WriteRequest
}

// JobSpecificRequest is used when we just need to specify a target job
type JobSpecificRequest struct {
	JobID string
	QueryOptions
}

// JobListRequest is used to parameterize a list request
type JobListRequest struct {
	QueryOptions
}

// NodeListRequest is used to parameterize a list request
type NodeListRequest struct {
	QueryOptions
}

// EvalUpdateRequest is used for upserting evaluations.
type EvalUpdateRequest struct {
	Evals     []*Evaluation
	EvalToken string
	WriteRequest
}

// EvalDeleteRequest is used for deleting an evaluation.
type EvalDeleteRequest struct {
	Evals  []string
	Allocs []string
	WriteRequest
}

// EvalSpecificRequest is used when we just need to specify a target evaluation
type EvalSpecificRequest struct {
	EvalID string
	QueryOptions
}

// EvalAckRequest is used to Ack/Nack a specific evaluation
type EvalAckRequest struct {
	EvalID string
	Token  string
	WriteRequest
}

// EvalDequeueRequest is used when we want to dequeue an evaluation
type EvalDequeueRequest struct {
	Schedulers []string
	Timeout    time.Duration
	WriteRequest
}

// EvalListRequest is used to list the evaluations
type EvalListRequest struct {
	QueryOptions
}

// PlanRequest is used to submit an allocation plan to the leader
type PlanRequest struct {
	Plan *Plan
	WriteRequest
}

// AllocUpdateRequest is used to submit changes to allocations, either
// to cause evictions or to assign new allocaitons. Both can be done
// within a single transaction
type AllocUpdateRequest struct {
	// Alloc is the list of new allocations to assign
	Alloc []*Allocation

	// Job is the shared parent job of the allocations.
	// It is pulled out since it is common to reduce payload size.
	Job *Job

	WriteRequest
}

// AllocListRequest is used to request a list of allocations
type AllocListRequest struct {
	QueryOptions
}

// AllocSpecificRequest is used to query a specific allocation
type AllocSpecificRequest struct {
	AllocID string
	QueryOptions
}

// AllocsGetcRequest is used to query a set of allocations
type AllocsGetRequest struct {
	AllocIDs []string
	QueryOptions
}

// PeriodicForceReqeuest is used to force a specific periodic job.
type PeriodicForceRequest struct {
	JobID string
	WriteRequest
}

// GenericRequest is used to request where no
// specific information is needed.
type GenericRequest struct {
	QueryOptions
}

// GenericResponse is used to respond to a request where no
// specific response information is needed.
type GenericResponse struct {
	WriteMeta
}

const (
	ProtocolVersion = "protocol"
	APIMajorVersion = "api.major"
	APIMinorVersion = "api.minor"
)

// VersionResponse is used for the Status.Version reseponse
type VersionResponse struct {
	Build    string
	Versions map[string]int
	QueryMeta
}

// JobRegisterResponse is used to respond to a job registration
type JobRegisterResponse struct {
	EvalID          string
	EvalCreateIndex uint64
	JobModifyIndex  uint64
	QueryMeta
}

// JobDeregisterResponse is used to respond to a job deregistration
type JobDeregisterResponse struct {
	EvalID          string
	EvalCreateIndex uint64
	JobModifyIndex  uint64
	QueryMeta
}

// NodeUpdateResponse is used to respond to a node update
type NodeUpdateResponse struct {
	HeartbeatTTL    time.Duration
	EvalIDs         []string
	EvalCreateIndex uint64
	NodeModifyIndex uint64
	QueryMeta
}

// NodeDrainUpdateResponse is used to respond to a node drain update
type NodeDrainUpdateResponse struct {
	EvalIDs         []string
	EvalCreateIndex uint64
	NodeModifyIndex uint64
	QueryMeta
}

// NodeAllocsResponse is used to return allocs for a single node
type NodeAllocsResponse struct {
	Allocs []*Allocation
	QueryMeta
}

// NodeClientAllocsResponse is used to return allocs meta data for a single node
type NodeClientAllocsResponse struct {
	Allocs map[string]uint64
	QueryMeta
}

// SingleNodeResponse is used to return a single node
type SingleNodeResponse struct {
	Node *Node
	QueryMeta
}

// JobListResponse is used for a list request
type NodeListResponse struct {
	Nodes []*NodeListStub
	QueryMeta
}

// SingleJobResponse is used to return a single job
type SingleJobResponse struct {
	Job *Job
	QueryMeta
}

// JobListResponse is used for a list request
type JobListResponse struct {
	Jobs []*JobListStub
	QueryMeta
}

// SingleAllocResponse is used to return a single allocation
type SingleAllocResponse struct {
	Alloc *Allocation
	QueryMeta
}

// AllocsGetResponse is used to return a set of allocations
type AllocsGetResponse struct {
	Allocs []*Allocation
	QueryMeta
}

// JobAllocationsResponse is used to return the allocations for a job
type JobAllocationsResponse struct {
	Allocations []*AllocListStub
	QueryMeta
}

// JobEvaluationsResponse is used to return the evaluations for a job
type JobEvaluationsResponse struct {
	Evaluations []*Evaluation
	QueryMeta
}

// SingleEvalResponse is used to return a single evaluation
type SingleEvalResponse struct {
	Eval *Evaluation
	QueryMeta
}

// EvalDequeueResponse is used to return from a dequeue
type EvalDequeueResponse struct {
	Eval  *Evaluation
	Token string
	QueryMeta
}

// PlanResponse is used to return from a PlanRequest
type PlanResponse struct {
	Result *PlanResult
	WriteMeta
}

// AllocListResponse is used for a list request
type AllocListResponse struct {
	Allocations []*AllocListStub
	QueryMeta
}

// EvalListResponse is used for a list request
type EvalListResponse struct {
	Evaluations []*Evaluation
	QueryMeta
}

// EvalAllocationsResponse is used to return the allocations for an evaluation
type EvalAllocationsResponse struct {
	Allocations []*AllocListStub
	QueryMeta
}

// PeriodicForceResponse is used to respond to a periodic job force launch
type PeriodicForceResponse struct {
	EvalID          string
	EvalCreateIndex uint64
	WriteMeta
}

const (
	NodeStatusInit  = "initializing"
	NodeStatusReady = "ready"
	NodeStatusDown  = "down"
)

// ShouldDrainNode checks if a given node status should trigger an
// evaluation. Some states don't require any further action.
func ShouldDrainNode(status string) bool {
	switch status {
	case NodeStatusInit, NodeStatusReady:
		return false
	case NodeStatusDown:
		return true
	default:
		panic(fmt.Sprintf("unhandled node status %s", status))
	}
}

// ValidNodeStatus is used to check if a node status is valid
func ValidNodeStatus(status string) bool {
	switch status {
	case NodeStatusInit, NodeStatusReady, NodeStatusDown:
		return true
	default:
		return false
	}
}

// Node is a representation of a schedulable client node
type Node struct {
	// ID is a unique identifier for the node. It can be constructed
	// by doing a concatenation of the Name and Datacenter as a simple
	// approach. Alternatively a UUID may be used.
	ID string

	// Datacenter for this node
	Datacenter string

	// Node name
	Name string

	// HTTPAddr is the address on which the Nomad client is listening for http
	// requests
	HTTPAddr string

	// Attributes is an arbitrary set of key/value
	// data that can be used for constraints. Examples
	// include "kernel.name=linux", "arch=386", "driver.docker=1",
	// "docker.runtime=1.8.3"
	Attributes map[string]string

	// Resources is the available resources on the client.
	// For example 'cpu=2' 'memory=2048'
	Resources *Resources

	// Reserved is the set of resources that are reserved,
	// and should be subtracted from the total resources for
	// the purposes of scheduling. This may be provide certain
	// high-watermark tolerances or because of external schedulers
	// consuming resources.
	Reserved *Resources

	// Links are used to 'link' this client to external
	// systems. For example 'consul=foo.dc1' 'aws=i-83212'
	// 'ami=ami-123'
	Links map[string]string

	// Meta is used to associate arbitrary metadata with this
	// client. This is opaque to Nomad.
	Meta map[string]string

	// NodeClass is an opaque identifier used to group nodes
	// together for the purpose of determining scheduling pressure.
	NodeClass string

	// ComputedClass is a unique id that identifies nodes with a common set of
	// attributes and capabilities.
	ComputedClass string

	// Drain is controlled by the servers, and not the client.
	// If true, no jobs will be scheduled to this node, and existing
	// allocations will be drained.
	Drain bool

	// Status of this node
	Status string

	// StatusDescription is meant to provide more human useful information
	StatusDescription string

	// Raft Indexes
	CreateIndex uint64
	ModifyIndex uint64
}

func (n *Node) Copy() *Node {
	if n == nil {
		return nil
	}
	nn := new(Node)
	*nn = *n
	nn.Attributes = CopyMapStringString(nn.Attributes)
	nn.Resources = nn.Resources.Copy()
	nn.Reserved = nn.Reserved.Copy()
	nn.Links = CopyMapStringString(nn.Links)
	nn.Meta = CopyMapStringString(nn.Meta)
	return nn
}

// TerminalStatus returns if the current status is terminal and
// will no longer transition.
func (n *Node) TerminalStatus() bool {
	switch n.Status {
	case NodeStatusDown:
		return true
	default:
		return false
	}
}

// Stub returns a summarized version of the node
func (n *Node) Stub() *NodeListStub {
	return &NodeListStub{
		ID:                n.ID,
		Datacenter:        n.Datacenter,
		Name:              n.Name,
		NodeClass:         n.NodeClass,
		Drain:             n.Drain,
		Status:            n.Status,
		StatusDescription: n.StatusDescription,
		CreateIndex:       n.CreateIndex,
		ModifyIndex:       n.ModifyIndex,
	}
}

// NodeListStub is used to return a subset of job information
// for the job list
type NodeListStub struct {
	ID                string
	Datacenter        string
	Name              string
	NodeClass         string
	Drain             bool
	Status            string
	StatusDescription string
	CreateIndex       uint64
	ModifyIndex       uint64
}

// Resources is used to define the resources available
// on a client
type Resources struct {
	CPU      int
	MemoryMB int `mapstructure:"memory"`
	DiskMB   int `mapstructure:"disk"`
	IOPS     int
	Networks []*NetworkResource
}

// DefaultResources returns the minimum resources a task can use and be valid.
func DefaultResources() *Resources {
	return &Resources{
		CPU:      100,
		MemoryMB: 10,
		DiskMB:   300,
		IOPS:     0,
	}
}

// Merge merges this resource with another resource.
func (r *Resources) Merge(other *Resources) {
	if other.CPU != 0 {
		r.CPU = other.CPU
	}
	if other.MemoryMB != 0 {
		r.MemoryMB = other.MemoryMB
	}
	if other.DiskMB != 0 {
		r.DiskMB = other.DiskMB
	}
	if other.IOPS != 0 {
		r.IOPS = other.IOPS
	}
	if len(other.Networks) != 0 {
		r.Networks = other.Networks
	}
}

// MeetsMinResources returns an error if the resources specified are less than
// the minimum allowed.
func (r *Resources) MeetsMinResources() error {
	var mErr multierror.Error
	if r.CPU < 20 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum CPU value is 20; got %d", r.CPU))
	}
	if r.MemoryMB < 10 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum MemoryMB value is 10; got %d", r.MemoryMB))
	}
	if r.DiskMB < 10 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum DiskMB value is 10; got %d", r.DiskMB))
	}
	if r.IOPS < 0 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum IOPS value is 0; got %d", r.IOPS))
	}
	for i, n := range r.Networks {
		if err := n.MeetsMinResources(); err != nil {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("network resource at index %d failed: %v", i, err))
		}
	}

	return mErr.ErrorOrNil()
}

// Copy returns a deep copy of the resources
func (r *Resources) Copy() *Resources {
	if r == nil {
		return nil
	}
	newR := new(Resources)
	*newR = *r
	n := len(r.Networks)
	newR.Networks = make([]*NetworkResource, n)
	for i := 0; i < n; i++ {
		newR.Networks[i] = r.Networks[i].Copy()
	}
	return newR
}

// NetIndex finds the matching net index using device name
func (r *Resources) NetIndex(n *NetworkResource) int {
	for idx, net := range r.Networks {
		if net.Device == n.Device {
			return idx
		}
	}
	return -1
}

// Superset checks if one set of resources is a superset
// of another. This ignores network resources, and the NetworkIndex
// should be used for that.
func (r *Resources) Superset(other *Resources) (bool, string) {
	if r.CPU < other.CPU {
		return false, "cpu exhausted"
	}
	if r.MemoryMB < other.MemoryMB {
		return false, "memory exhausted"
	}
	if r.DiskMB < other.DiskMB {
		return false, "disk exhausted"
	}
	if r.IOPS < other.IOPS {
		return false, "iops exhausted"
	}
	return true, ""
}

// Add adds the resources of the delta to this, potentially
// returning an error if not possible.
func (r *Resources) Add(delta *Resources) error {
	if delta == nil {
		return nil
	}
	r.CPU += delta.CPU
	r.MemoryMB += delta.MemoryMB
	r.DiskMB += delta.DiskMB
	r.IOPS += delta.IOPS

	for _, n := range delta.Networks {
		// Find the matching interface by IP or CIDR
		idx := r.NetIndex(n)
		if idx == -1 {
			r.Networks = append(r.Networks, n.Copy())
		} else {
			r.Networks[idx].Add(n)
		}
	}
	return nil
}

func (r *Resources) GoString() string {
	return fmt.Sprintf("*%#v", *r)
}

type Port struct {
	Label string
	Value int `mapstructure:"static"`
}

// NetworkResource is used to represent available network
// resources
type NetworkResource struct {
	Device        string // Name of the device
	CIDR          string // CIDR block of addresses
	IP            string // IP address
	MBits         int    // Throughput
	ReservedPorts []Port // Reserved ports
	DynamicPorts  []Port // Dynamically assigned ports
}

// MeetsMinResources returns an error if the resources specified are less than
// the minimum allowed.
func (n *NetworkResource) MeetsMinResources() error {
	var mErr multierror.Error
	if n.MBits < 1 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum MBits value is 1; got %d", n.MBits))
	}
	return mErr.ErrorOrNil()
}

// Copy returns a deep copy of the network resource
func (n *NetworkResource) Copy() *NetworkResource {
	if n == nil {
		return nil
	}
	newR := new(NetworkResource)
	*newR = *n
	if n.ReservedPorts != nil {
		newR.ReservedPorts = make([]Port, len(n.ReservedPorts))
		copy(newR.ReservedPorts, n.ReservedPorts)
	}
	if n.DynamicPorts != nil {
		newR.DynamicPorts = make([]Port, len(n.DynamicPorts))
		copy(newR.DynamicPorts, n.DynamicPorts)
	}
	return newR
}

// Add adds the resources of the delta to this, potentially
// returning an error if not possible.
func (n *NetworkResource) Add(delta *NetworkResource) {
	if len(delta.ReservedPorts) > 0 {
		n.ReservedPorts = append(n.ReservedPorts, delta.ReservedPorts...)
	}
	n.MBits += delta.MBits
	n.DynamicPorts = append(n.DynamicPorts, delta.DynamicPorts...)
}

func (n *NetworkResource) GoString() string {
	return fmt.Sprintf("*%#v", *n)
}

func (n *NetworkResource) MapLabelToValues(port_map map[string]int) map[string]int {
	labelValues := make(map[string]int)
	ports := append(n.ReservedPorts, n.DynamicPorts...)
	for _, port := range ports {
		if mapping, ok := port_map[port.Label]; ok {
			labelValues[port.Label] = mapping
		} else {
			labelValues[port.Label] = port.Value
		}
	}
	return labelValues
}

const (
	// JobTypeNomad is reserved for internal system tasks and is
	// always handled by the CoreScheduler.
	JobTypeCore    = "_core"
	JobTypeService = "service"
	JobTypeBatch   = "batch"
	JobTypeSystem  = "system"
)

const (
	JobStatusPending = "pending" // Pending means the job is waiting on scheduling
	JobStatusRunning = "running" // Running means the job has non-terminal allocations
	JobStatusDead    = "dead"    // Dead means all evaluation's and allocations are terminal
)

const (
	// JobMinPriority is the minimum allowed priority
	JobMinPriority = 1

	// JobDefaultPriority is the default priority if not
	// not specified.
	JobDefaultPriority = 50

	// JobMaxPriority is the maximum allowed priority
	JobMaxPriority = 100

	// Ensure CoreJobPriority is higher than any user
	// specified job so that it gets priority. This is important
	// for the system to remain healthy.
	CoreJobPriority = JobMaxPriority * 2
)

// Job is the scope of a scheduling request to Nomad. It is the largest
// scoped object, and is a named collection of task groups. Each task group
// is further composed of tasks. A task group (TG) is the unit of scheduling
// however.
type Job struct {
	// Region is the Nomad region that handles scheduling this job
	Region string

	// ID is a unique identifier for the job per region. It can be
	// specified hierarchically like LineOfBiz/OrgName/Team/Project
	ID string

	// ParentID is the unique identifier of the job that spawned this job.
	ParentID string

	// Name is the logical name of the job used to refer to it. This is unique
	// per region, but not unique globally.
	Name string

	// Type is used to control various behaviors about the job. Most jobs
	// are service jobs, meaning they are expected to be long lived.
	// Some jobs are batch oriented meaning they run and then terminate.
	// This can be extended in the future to support custom schedulers.
	Type string

	// Priority is used to control scheduling importance and if this job
	// can preempt other jobs.
	Priority int

	// AllAtOnce is used to control if incremental scheduling of task groups
	// is allowed or if we must do a gang scheduling of the entire job. This
	// can slow down larger jobs if resources are not available.
	AllAtOnce bool `mapstructure:"all_at_once"`

	// Datacenters contains all the datacenters this job is allowed to span
	Datacenters []string

	// Constraints can be specified at a job level and apply to
	// all the task groups and tasks.
	Constraints []*Constraint

	// TaskGroups are the collections of task groups that this job needs
	// to run. Each task group is an atomic unit of scheduling and placement.
	TaskGroups []*TaskGroup

	// Update is used to control the update strategy
	Update UpdateStrategy

	// Periodic is used to define the interval the job is run at.
	Periodic *PeriodicConfig

	// GC is used to mark the job as available for garbage collection after it
	// has no outstanding evaluations or allocations.
	GC bool

	// Meta is used to associate arbitrary metadata with this
	// job. This is opaque to Nomad.
	Meta map[string]string

	// Job status
	Status string

	// StatusDescription is meant to provide more human useful information
	StatusDescription string

	// Raft Indexes
	CreateIndex    uint64
	ModifyIndex    uint64
	JobModifyIndex uint64
}

// InitFields is used to initialize fields in the Job. This should be called
// when registering a Job.
func (j *Job) InitFields() {
	for _, tg := range j.TaskGroups {
		tg.InitFields(j)
	}

	// If the job is batch then make it GC.
	if j.Type == JobTypeBatch {
		j.GC = true
	}
}

// Copy returns a deep copy of the Job. It is expected that callers use recover.
// This job can panic if the deep copy failed as it uses reflection.
func (j *Job) Copy() *Job {
	if j == nil {
		return nil
	}
	nj := new(Job)
	*nj = *j
	nj.Datacenters = CopySliceString(nj.Datacenters)
	nj.Constraints = CopySliceConstraints(nj.Constraints)

	tgs := make([]*TaskGroup, len(nj.TaskGroups))
	for i, tg := range nj.TaskGroups {
		tgs[i] = tg.Copy()
	}
	nj.TaskGroups = tgs

	nj.Periodic = nj.Periodic.Copy()
	nj.Meta = CopyMapStringString(nj.Meta)
	return nj
}

// Validate is used to sanity check a job input
func (j *Job) Validate() error {
	var mErr multierror.Error
	if j.Region == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing job region"))
	}
	if j.ID == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing job ID"))
	} else if strings.Contains(j.ID, " ") {
		mErr.Errors = append(mErr.Errors, errors.New("Job ID contains a space"))
	}
	if j.Name == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing job name"))
	}
	if j.Type == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing job type"))
	}
	if j.Priority < JobMinPriority || j.Priority > JobMaxPriority {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("Job priority must be between [%d, %d]", JobMinPriority, JobMaxPriority))
	}
	if len(j.Datacenters) == 0 {
		mErr.Errors = append(mErr.Errors, errors.New("Missing job datacenters"))
	}
	if len(j.TaskGroups) == 0 {
		mErr.Errors = append(mErr.Errors, errors.New("Missing job task groups"))
	}
	for idx, constr := range j.Constraints {
		if err := constr.Validate(); err != nil {
			outer := fmt.Errorf("Constraint %d validation failed: %s", idx+1, err)
			mErr.Errors = append(mErr.Errors, outer)
		}
	}

	// Check for duplicate task groups
	taskGroups := make(map[string]int)
	for idx, tg := range j.TaskGroups {
		if tg.Name == "" {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("Job task group %d missing name", idx+1))
		} else if existing, ok := taskGroups[tg.Name]; ok {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("Job task group %d redefines '%s' from group %d", idx+1, tg.Name, existing+1))
		} else {
			taskGroups[tg.Name] = idx
		}

		if j.Type == "system" && tg.Count != 1 {
			mErr.Errors = append(mErr.Errors,
				fmt.Errorf("Job task group %d has count %d. Only count of 1 is supported with system scheduler",
					idx+1, tg.Count))
		}
	}

	// Validate the task group
	for idx, tg := range j.TaskGroups {
		if err := tg.Validate(); err != nil {
			outer := fmt.Errorf("Task group %d validation failed: %s", idx+1, err)
			mErr.Errors = append(mErr.Errors, outer)
		}
	}

	// Validate periodic is only used with batch jobs.
	if j.IsPeriodic() {
		if j.Type != JobTypeBatch {
			mErr.Errors = append(mErr.Errors,
				fmt.Errorf("Periodic can only be used with %q scheduler", JobTypeBatch))
		}

		if err := j.Periodic.Validate(); err != nil {
			mErr.Errors = append(mErr.Errors, err)
		}
	}

	return mErr.ErrorOrNil()
}

// LookupTaskGroup finds a task group by name
func (j *Job) LookupTaskGroup(name string) *TaskGroup {
	for _, tg := range j.TaskGroups {
		if tg.Name == name {
			return tg
		}
	}
	return nil
}

// Stub is used to return a summary of the job
func (j *Job) Stub() *JobListStub {
	return &JobListStub{
		ID:                j.ID,
		ParentID:          j.ParentID,
		Name:              j.Name,
		Type:              j.Type,
		Priority:          j.Priority,
		Status:            j.Status,
		StatusDescription: j.StatusDescription,
		CreateIndex:       j.CreateIndex,
		ModifyIndex:       j.ModifyIndex,
	}
}

// IsPeriodic returns whether a job is periodic.
func (j *Job) IsPeriodic() bool {
	return j.Periodic != nil
}

// JobListStub is used to return a subset of job information
// for the job list
type JobListStub struct {
	ID                string
	ParentID          string
	Name              string
	Type              string
	Priority          int
	Status            string
	StatusDescription string
	CreateIndex       uint64
	ModifyIndex       uint64
}

// UpdateStrategy is used to modify how updates are done
type UpdateStrategy struct {
	// Stagger is the amount of time between the updates
	Stagger time.Duration

	// MaxParallel is how many updates can be done in parallel
	MaxParallel int `mapstructure:"max_parallel"`
}

// Rolling returns if a rolling strategy should be used
func (u *UpdateStrategy) Rolling() bool {
	return u.Stagger > 0 && u.MaxParallel > 0
}

const (
	// PeriodicSpecCron is used for a cron spec.
	PeriodicSpecCron = "cron"

	// PeriodicSpecTest is only used by unit tests. It is a sorted, comma
	// seperated list of unix timestamps at which to launch.
	PeriodicSpecTest = "_internal_test"
)

// Periodic defines the interval a job should be run at.
type PeriodicConfig struct {
	// Enabled determines if the job should be run periodically.
	Enabled bool

	// Spec specifies the interval the job should be run as. It is parsed based
	// on the SpecType.
	Spec string

	// SpecType defines the format of the spec.
	SpecType string

	// ProhibitOverlap enforces that spawned jobs do not run in parallel.
	ProhibitOverlap bool `mapstructure:"prohibit_overlap"`
}

func (p *PeriodicConfig) Copy() *PeriodicConfig {
	if p == nil {
		return nil
	}
	np := new(PeriodicConfig)
	*np = *p
	return np
}

func (p *PeriodicConfig) Validate() error {
	if !p.Enabled {
		return nil
	}

	if p.Spec == "" {
		return fmt.Errorf("Must specify a spec")
	}

	switch p.SpecType {
	case PeriodicSpecCron:
		// Validate the cron spec
		if _, err := cronexpr.Parse(p.Spec); err != nil {
			return fmt.Errorf("Invalid cron spec %q: %v", p.Spec, err)
		}
	case PeriodicSpecTest:
		// No-op
	default:
		return fmt.Errorf("Unknown periodic specification type %q", p.SpecType)
	}

	return nil
}

// Next returns the closest time instant matching the spec that is after the
// passed time. If no matching instance exists, the zero value of time.Time is
// returned. The `time.Location` of the returned value matches that of the
// passed time.
func (p *PeriodicConfig) Next(fromTime time.Time) time.Time {
	switch p.SpecType {
	case PeriodicSpecCron:
		if e, err := cronexpr.Parse(p.Spec); err == nil {
			return e.Next(fromTime)
		}
	case PeriodicSpecTest:
		split := strings.Split(p.Spec, ",")
		if len(split) == 1 && split[0] == "" {
			return time.Time{}
		}

		// Parse the times
		times := make([]time.Time, len(split))
		for i, s := range split {
			unix, err := strconv.Atoi(s)
			if err != nil {
				return time.Time{}
			}

			times[i] = time.Unix(int64(unix), 0)
		}

		// Find the next match
		for _, next := range times {
			if fromTime.Before(next) {
				return next
			}
		}
	}

	return time.Time{}
}

const (
	// PeriodicLaunchSuffix is the string appended to the periodic jobs ID
	// when launching derived instances of it.
	PeriodicLaunchSuffix = "/periodic-"
)

// PeriodicLaunch tracks the last launch time of a periodic job.
type PeriodicLaunch struct {
	ID     string    // ID of the periodic job.
	Launch time.Time // The last launch time.

	// Raft Indexes
	CreateIndex uint64
	ModifyIndex uint64
}

var (
	defaultServiceJobRestartPolicy = RestartPolicy{
		Delay:    15 * time.Second,
		Attempts: 2,
		Interval: 1 * time.Minute,
		Mode:     RestartPolicyModeDelay,
	}
	defaultBatchJobRestartPolicy = RestartPolicy{
		Delay:    15 * time.Second,
		Attempts: 15,
		Interval: 7 * 24 * time.Hour,
		Mode:     RestartPolicyModeDelay,
	}
)

const (
	// RestartPolicyModeDelay causes an artificial delay till the next interval is
	// reached when the specified attempts have been reached in the interval.
	RestartPolicyModeDelay = "delay"

	// RestartPolicyModeFail causes a job to fail if the specified number of
	// attempts are reached within an interval.
	RestartPolicyModeFail = "fail"
)

// RestartPolicy configures how Tasks are restarted when they crash or fail.
type RestartPolicy struct {
	// Attempts is the number of restart that will occur in an interval.
	Attempts int

	// Interval is a duration in which we can limit the number of restarts
	// within.
	Interval time.Duration

	// Delay is the time between a failure and a restart.
	Delay time.Duration

	// Mode controls what happens when the task restarts more than attempt times
	// in an interval.
	Mode string
}

func (r *RestartPolicy) Copy() *RestartPolicy {
	if r == nil {
		return nil
	}
	nrp := new(RestartPolicy)
	*nrp = *r
	return nrp
}

func (r *RestartPolicy) Validate() error {
	switch r.Mode {
	case RestartPolicyModeDelay, RestartPolicyModeFail:
	default:
		return fmt.Errorf("Unsupported restart mode: %q", r.Mode)
	}

	// Check for ambiguous/confusing settings
	if r.Attempts == 0 && r.Mode != RestartPolicyModeFail {
		return fmt.Errorf("Restart policy %q with %d attempts is ambiguous", r.Mode, r.Attempts)
	}

	if r.Interval == 0 {
		return nil
	}
	if time.Duration(r.Attempts)*r.Delay > r.Interval {
		return fmt.Errorf("Nomad can't restart the TaskGroup %v times in an interval of %v with a delay of %v", r.Attempts, r.Interval, r.Delay)
	}
	return nil
}

func NewRestartPolicy(jobType string) *RestartPolicy {
	switch jobType {
	case JobTypeService, JobTypeSystem:
		rp := defaultServiceJobRestartPolicy
		return &rp
	case JobTypeBatch:
		rp := defaultBatchJobRestartPolicy
		return &rp
	}
	return nil
}

// TaskGroup is an atomic unit of placement. Each task group belongs to
// a job and may contain any number of tasks. A task group support running
// in many replicas using the same configuration..
type TaskGroup struct {
	// Name of the task group
	Name string

	// Count is the number of replicas of this task group that should
	// be scheduled.
	Count int

	// Constraints can be specified at a task group level and apply to
	// all the tasks contained.
	Constraints []*Constraint

	//RestartPolicy of a TaskGroup
	RestartPolicy *RestartPolicy

	// Tasks are the collection of tasks that this task group needs to run
	Tasks []*Task

	// Meta is used to associate arbitrary metadata with this
	// task group. This is opaque to Nomad.
	Meta map[string]string
}

func (tg *TaskGroup) Copy() *TaskGroup {
	if tg == nil {
		return nil
	}
	ntg := new(TaskGroup)
	*ntg = *tg
	ntg.Constraints = CopySliceConstraints(ntg.Constraints)

	ntg.RestartPolicy = ntg.RestartPolicy.Copy()

	tasks := make([]*Task, len(ntg.Tasks))
	for i, t := range ntg.Tasks {
		tasks[i] = t.Copy()
	}
	ntg.Tasks = tasks

	ntg.Meta = CopyMapStringString(ntg.Meta)
	return ntg
}

// InitFields is used to initialize fields in the TaskGroup.
func (tg *TaskGroup) InitFields(job *Job) {
	// Set the default restart policy.
	if tg.RestartPolicy == nil {
		tg.RestartPolicy = NewRestartPolicy(job.Type)
	}

	for _, task := range tg.Tasks {
		task.InitFields(job, tg)
	}
}

// Validate is used to sanity check a task group
func (tg *TaskGroup) Validate() error {
	var mErr multierror.Error
	if tg.Name == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing task group name"))
	}
	if tg.Count <= 0 {
		mErr.Errors = append(mErr.Errors, errors.New("Task group count must be positive"))
	}
	if len(tg.Tasks) == 0 {
		mErr.Errors = append(mErr.Errors, errors.New("Missing tasks for task group"))
	}
	for idx, constr := range tg.Constraints {
		if err := constr.Validate(); err != nil {
			outer := fmt.Errorf("Constraint %d validation failed: %s", idx+1, err)
			mErr.Errors = append(mErr.Errors, outer)
		}
	}

	if tg.RestartPolicy != nil {
		if err := tg.RestartPolicy.Validate(); err != nil {
			mErr.Errors = append(mErr.Errors, err)
		}
	} else {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("Task Group %v should have a restart policy", tg.Name))
	}

	// Check for duplicate tasks
	tasks := make(map[string]int)
	for idx, task := range tg.Tasks {
		if task.Name == "" {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("Task %d missing name", idx+1))
		} else if existing, ok := tasks[task.Name]; ok {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("Task %d redefines '%s' from task %d", idx+1, task.Name, existing+1))
		} else {
			tasks[task.Name] = idx
		}
	}

	// Validate the tasks
	for idx, task := range tg.Tasks {
		if err := task.Validate(); err != nil {
			outer := fmt.Errorf("Task %d validation failed: %s", idx+1, err)
			mErr.Errors = append(mErr.Errors, outer)
		}
	}
	return mErr.ErrorOrNil()
}

// LookupTask finds a task by name
func (tg *TaskGroup) LookupTask(name string) *Task {
	for _, t := range tg.Tasks {
		if t.Name == name {
			return t
		}
	}
	return nil
}

func (tg *TaskGroup) GoString() string {
	return fmt.Sprintf("*%#v", *tg)
}

const (
	ServiceCheckHTTP   = "http"
	ServiceCheckTCP    = "tcp"
	ServiceCheckDocker = "docker"
	ServiceCheckScript = "script"
)

// The ServiceCheck data model represents the consul health check that
// Nomad registers for a Task
type ServiceCheck struct {
	Name     string        // Name of the check, defaults to id
	Type     string        // Type of the check - tcp, http, docker and script
	Script   string        // Script to invoke for script check
	Path     string        // path of the health check url for http type check
	Protocol string        // Protocol to use if check is http, defaults to http
	Interval time.Duration // Interval of the check
	Timeout  time.Duration // Timeout of the response from the check before consul fails the check
}

func (sc *ServiceCheck) Copy() *ServiceCheck {
	if sc == nil {
		return nil
	}
	nsc := new(ServiceCheck)
	*nsc = *sc
	return nsc
}

func (sc *ServiceCheck) Validate() error {
	t := strings.ToLower(sc.Type)
	if t != ServiceCheckTCP && t != ServiceCheckHTTP {
		return fmt.Errorf("service check must be either http or tcp type")
	}
	if sc.Type == ServiceCheckHTTP && sc.Path == "" {
		return fmt.Errorf("service checks of http type must have a valid http path")
	}

	if sc.Type == ServiceCheckScript && sc.Script == "" {
		return fmt.Errorf("service checks of script type must have a valid script path")
	}

	if sc.Interval <= 0 {
		return fmt.Errorf("service checks must have positive time intervals")
	}
	return nil
}

func (sc *ServiceCheck) Hash(serviceID string) string {
	h := sha1.New()
	io.WriteString(h, serviceID)
	io.WriteString(h, sc.Name)
	io.WriteString(h, sc.Type)
	io.WriteString(h, sc.Script)
	io.WriteString(h, sc.Path)
	io.WriteString(h, sc.Path)
	io.WriteString(h, sc.Protocol)
	io.WriteString(h, sc.Interval.String())
	io.WriteString(h, sc.Timeout.String())
	return fmt.Sprintf("%x", h.Sum(nil))
}

const (
	NomadConsulPrefix = "nomad-registered-service"
)

// The Service model represents a Consul service defintion
type Service struct {
	Name      string          // Name of the service, defaults to id
	Tags      []string        // List of tags for the service
	PortLabel string          `mapstructure:"port"` // port for the service
	Checks    []*ServiceCheck // List of checks associated with the service
}

func (s *Service) Copy() *Service {
	if s == nil {
		return nil
	}
	ns := new(Service)
	*ns = *s
	ns.Tags = CopySliceString(ns.Tags)

	var checks []*ServiceCheck
	if l := len(ns.Checks); l != 0 {
		checks = make([]*ServiceCheck, len(ns.Checks))
		for i, c := range ns.Checks {
			checks[i] = c.Copy()
		}
	}
	ns.Checks = checks
	return ns
}

// InitFields interpolates values of Job, Task Group and Task in the Service
// Name. This also generates check names, service id and check ids.
func (s *Service) InitFields(job string, taskGroup string, task string) {
	s.Name = args.ReplaceEnv(s.Name, map[string]string{
		"JOB":       job,
		"TASKGROUP": taskGroup,
		"TASK":      task,
		"BASE":      fmt.Sprintf("%s-%s-%s", job, taskGroup, task),
	},
	)

	for _, check := range s.Checks {
		if check.Name == "" {
			check.Name = fmt.Sprintf("service: %q check", s.Name)
		}
	}
}

// Validate checks if the Check definition is valid
func (s *Service) Validate() error {
	var mErr multierror.Error

	// Ensure the name does not have a period in it.
	// RFC-2782: https://tools.ietf.org/html/rfc2782
	if strings.Contains(s.Name, ".") {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("service name can't contain periods: %q", s.Name))
	}

	for _, c := range s.Checks {
		if err := c.Validate(); err != nil {
			mErr.Errors = append(mErr.Errors, err)
		}
	}
	return mErr.ErrorOrNil()
}

// Hash calculates the hash of the check based on it's content and the service
// which owns it
func (s *Service) Hash() string {
	h := sha1.New()
	io.WriteString(h, s.Name)
	io.WriteString(h, strings.Join(s.Tags, ""))
	io.WriteString(h, s.PortLabel)
	return fmt.Sprintf("%x", h.Sum(nil))
}

const (
	// DefaultKillTimeout is the default timeout between signaling a task it
	// will be killed and killing it.
	DefaultKillTimeout = 5 * time.Second
)

// LogConfig provides configuration for log rotation
type LogConfig struct {
	MaxFiles      int `mapstructure:"max_files"`
	MaxFileSizeMB int `mapstructure:"max_file_size"`
}

func DefaultLogConfig() *LogConfig {
	return &LogConfig{
		MaxFiles:      10,
		MaxFileSizeMB: 10,
	}
}

// Validate returns an error if the log config specified are less than
// the minimum allowed.
func (l *LogConfig) Validate() error {
	var mErr multierror.Error
	if l.MaxFiles < 1 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum number of files is 1; got %d", l.MaxFiles))
	}
	if l.MaxFileSizeMB < 1 {
		mErr.Errors = append(mErr.Errors, fmt.Errorf("minimum file size is 1MB; got %d", l.MaxFileSizeMB))
	}
	return mErr.ErrorOrNil()
}

// Task is a single process typically that is executed as part of a task group.
type Task struct {
	// Name of the task
	Name string

	// Driver is used to control which driver is used
	Driver string

	// Config is provided to the driver to initialize
	Config map[string]interface{}

	// Map of environment variables to be used by the driver
	Env map[string]string

	// List of service definitions exposed by the Task
	Services []*Service

	// Constraints can be specified at a task level and apply only to
	// the particular task.
	Constraints []*Constraint

	// Resources is the resources needed by this task
	Resources *Resources

	// Meta is used to associate arbitrary metadata with this
	// task. This is opaque to Nomad.
	Meta map[string]string

	// KillTimeout is the time between signaling a task that it will be
	// killed and killing it.
	KillTimeout time.Duration `mapstructure:"kill_timeout"`

	// LogConfig provides configuration for log rotation
	LogConfig *LogConfig `mapstructure:"logs"`
}

func (t *Task) Copy() *Task {
	if t == nil {
		return nil
	}
	nt := new(Task)
	*nt = *t
	nt.Env = CopyMapStringString(nt.Env)

	services := make([]*Service, len(nt.Services))
	for i, s := range nt.Services {
		services[i] = s.Copy()
	}
	nt.Services = services
	nt.Constraints = CopySliceConstraints(nt.Constraints)

	nt.Resources = nt.Resources.Copy()
	nt.Meta = CopyMapStringString(nt.Meta)

	if i, err := copystructure.Copy(nt.Config); err != nil {
		nt.Config = i.(map[string]interface{})
	}

	return nt
}

// InitFields initializes fields in the task.
func (t *Task) InitFields(job *Job, tg *TaskGroup) {
	t.InitServiceFields(job.Name, tg.Name)

	// Set the default timeout if it is not specified.
	if t.KillTimeout == 0 {
		t.KillTimeout = DefaultKillTimeout
	}
}

// InitServiceFields interpolates values of Job, Task Group
// and Tasks in all the service Names of a Task. This also generates the service
// id, check id and check names.
func (t *Task) InitServiceFields(job string, taskGroup string) {
	for _, service := range t.Services {
		service.InitFields(job, taskGroup, t.Name)
	}
}

func (t *Task) GoString() string {
	return fmt.Sprintf("*%#v", *t)
}

func (t *Task) FindHostAndPortFor(portLabel string) (string, int) {
	for _, network := range t.Resources.Networks {
		if p, ok := network.MapLabelToValues(nil)[portLabel]; ok {
			return network.IP, p
		}
	}
	return "", 0
}

// Set of possible states for a task.
const (
	TaskStatePending = "pending" // The task is waiting to be run.
	TaskStateRunning = "running" // The task is currently running.
	TaskStateDead    = "dead"    // Terminal state of task.
)

// TaskState tracks the current state of a task and events that caused state
// transistions.
type TaskState struct {
	// The current state of the task.
	State string

	// Series of task events that transistion the state of the task.
	Events []*TaskEvent
}

func (ts *TaskState) Copy() *TaskState {
	if ts == nil {
		return nil
	}
	copy := new(TaskState)
	copy.State = ts.State
	copy.Events = make([]*TaskEvent, len(ts.Events))
	for i, e := range ts.Events {
		copy.Events[i] = e.Copy()
	}
	return copy
}

// Failed returns if the task has has failed.
func (ts *TaskState) Failed() bool {
	l := len(ts.Events)
	if ts.State != TaskStateDead || l == 0 {
		return false
	}

	return ts.Events[l-1].Type == TaskNotRestarting
}

const (
	// A Driver failure indicates that the task could not be started due to a
	// failure in the driver.
	TaskDriverFailure = "Driver Failure"

	// Task Received signals that the task has been pulled by the client at the
	// given timestamp.
	TaskReceived = "Received"

	// Task Started signals that the task was started and its timestamp can be
	// used to determine the running length of the task.
	TaskStarted = "Started"

	// Task terminated indicates that the task was started and exited.
	TaskTerminated = "Terminated"

	// Task Killed indicates a user has killed the task.
	TaskKilled = "Killed"

	// TaskRestarting indicates that task terminated and is being restarted.
	TaskRestarting = "Restarting"

	// TaskNotRestarting indicates that the task has failed and is not being
	// restarted because it has exceeded its restart policy.
	TaskNotRestarting = "Restarts Exceeded"
)

// TaskEvent is an event that effects the state of a task and contains meta-data
// appropriate to the events type.
type TaskEvent struct {
	Type string
	Time int64 // Unix Nanosecond timestamp

	// Driver Failure fields.
	DriverError string // A driver error occured while starting the task.

	// Task Terminated Fields.
	ExitCode int    // The exit code of the task.
	Signal   int    // The signal that terminated the task.
	Message  string // A possible message explaining the termination of the task.

	// Task Killed Fields.
	KillError string // Error killing the task.

	// TaskRestarting fields.
	StartDelay int64 // The sleep period before restarting the task in unix nanoseconds.
}

func (te *TaskEvent) GoString() string {
	return fmt.Sprintf("%v at %v", te.Type, te.Time)
}

func (te *TaskEvent) Copy() *TaskEvent {
	if te == nil {
		return nil
	}
	copy := new(TaskEvent)
	*copy = *te
	return copy
}

func NewTaskEvent(event string) *TaskEvent {
	return &TaskEvent{
		Type: event,
		Time: time.Now().UnixNano(),
	}
}

func (e *TaskEvent) SetDriverError(err error) *TaskEvent {
	if err != nil {
		e.DriverError = err.Error()
	}
	return e
}

func (e *TaskEvent) SetExitCode(c int) *TaskEvent {
	e.ExitCode = c
	return e
}

func (e *TaskEvent) SetSignal(s int) *TaskEvent {
	e.Signal = s
	return e
}

func (e *TaskEvent) SetExitMessage(err error) *TaskEvent {
	if err != nil {
		e.Message = err.Error()
	}
	return e
}

func (e *TaskEvent) SetKillError(err error) *TaskEvent {
	if err != nil {
		e.KillError = err.Error()
	}
	return e
}

func (e *TaskEvent) SetRestartDelay(delay time.Duration) *TaskEvent {
	e.StartDelay = int64(delay)
	return e
}

// Validate is used to sanity check a task group
func (t *Task) Validate() error {
	var mErr multierror.Error
	if t.Name == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing task name"))
	}
	if t.Driver == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing task driver"))
	}
	if t.KillTimeout.Nanoseconds() < 0 {
		mErr.Errors = append(mErr.Errors, errors.New("KillTimeout must be a positive value"))
	}

	// Validate the resources.
	if t.Resources == nil {
		mErr.Errors = append(mErr.Errors, errors.New("Missing task resources"))
	} else if err := t.Resources.MeetsMinResources(); err != nil {
		mErr.Errors = append(mErr.Errors, err)
	}

	// Validate the log config
	if t.LogConfig == nil {
		mErr.Errors = append(mErr.Errors, errors.New("Missing Log Config"))
	} else if err := t.LogConfig.Validate(); err != nil {
		mErr.Errors = append(mErr.Errors, err)
	}

	for idx, constr := range t.Constraints {
		if err := constr.Validate(); err != nil {
			outer := fmt.Errorf("Constraint %d validation failed: %s", idx+1, err)
			mErr.Errors = append(mErr.Errors, outer)
		}
	}

	for _, service := range t.Services {
		if err := service.Validate(); err != nil {
			mErr.Errors = append(mErr.Errors, err)
		}
	}

	if t.LogConfig != nil && t.Resources != nil {
		logUsage := (t.LogConfig.MaxFiles * t.LogConfig.MaxFileSizeMB)
		if t.Resources.DiskMB <= logUsage {
			mErr.Errors = append(mErr.Errors,
				fmt.Errorf("log storage (%d MB) exceeds requested disk capacity (%d MB)",
					logUsage, t.Resources.DiskMB))
		}
	}
	return mErr.ErrorOrNil()
}

const (
	ConstraintDistinctHosts = "distinct_hosts"
	ConstraintRegex         = "regexp"
	ConstraintVersion       = "version"
)

// Constraints are used to restrict placement options.
type Constraint struct {
	LTarget string // Left-hand target
	RTarget string // Right-hand target
	Operand string // Constraint operand (<=, <, =, !=, >, >=), contains, near
	str     string // Memoized string
}

func (c *Constraint) Copy() *Constraint {
	if c == nil {
		return nil
	}
	nc := new(Constraint)
	*nc = *c
	return nc
}

func (c *Constraint) String() string {
	if c.str != "" {
		return c.str
	}
	c.str = fmt.Sprintf("%s %s %s", c.LTarget, c.Operand, c.RTarget)
	return c.str
}

func (c *Constraint) Validate() error {
	var mErr multierror.Error
	if c.Operand == "" {
		mErr.Errors = append(mErr.Errors, errors.New("Missing constraint operand"))
	}

	// Perform additional validation based on operand
	switch c.Operand {
	case ConstraintRegex:
		if _, err := regexp.Compile(c.RTarget); err != nil {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("Regular expression failed to compile: %v", err))
		}
	case ConstraintVersion:
		if _, err := version.NewConstraint(c.RTarget); err != nil {
			mErr.Errors = append(mErr.Errors, fmt.Errorf("Version constraint is invalid: %v", err))
		}
	}
	return mErr.ErrorOrNil()
}

const (
	AllocDesiredStatusRun    = "run"    // Allocation should run
	AllocDesiredStatusStop   = "stop"   // Allocation should stop
	AllocDesiredStatusEvict  = "evict"  // Allocation should stop, and was evicted
	AllocDesiredStatusFailed = "failed" // Allocation failed to be done
)

const (
	AllocClientStatusPending = "pending"
	AllocClientStatusRunning = "running"
	AllocClientStatusDead    = "dead"
	AllocClientStatusFailed  = "failed"
)

// Allocation is used to allocate the placement of a task group to a node.
type Allocation struct {
	// ID of the allocation (UUID)
	ID string

	// ID of the evaluation that generated this allocation
	EvalID string

	// Name is a logical name of the allocation.
	Name string

	// NodeID is the node this is being placed on
	NodeID string

	// Job is the parent job of the task group being allocated.
	// This is copied at allocation time to avoid issues if the job
	// definition is updated.
	JobID string
	Job   *Job

	// TaskGroup is the name of the task group that should be run
	TaskGroup string

	// Resources is the total set of resources allocated as part
	// of this allocation of the task group.
	Resources *Resources

	// TaskResources is the set of resources allocated to each
	// task. These should sum to the total Resources.
	TaskResources map[string]*Resources

	// Services is a map of service names to service ids
	Services map[string]string

	// Metrics associated with this allocation
	Metrics *AllocMetric

	// Desired Status of the allocation on the client
	DesiredStatus string

	// DesiredStatusDescription is meant to provide more human useful information
	DesiredDescription string

	// Status of the allocation on the client
	ClientStatus string

	// ClientStatusDescription is meant to provide more human useful information
	ClientDescription string

	// TaskStates stores the state of each task,
	TaskStates map[string]*TaskState

	// Raft Indexes
	CreateIndex uint64
	ModifyIndex uint64

	// AllocModifyIndex is not updated when the client updates allocations. This
	// lets the client pull only the allocs updated by the server.
	AllocModifyIndex uint64

	// CreateTime is the time the allocation has finished scheduling and been
	// verified by the plan applier.
	CreateTime int64
}

func (a *Allocation) Copy() *Allocation {
	if a == nil {
		return nil
	}
	na := new(Allocation)
	*na = *a

	na.Job = na.Job.Copy()
	na.Resources = na.Resources.Copy()

	tr := make(map[string]*Resources, len(na.TaskResources))
	for task, resource := range na.TaskResources {
		tr[task] = resource.Copy()
	}
	na.TaskResources = tr

	s := make(map[string]string, len(na.Services))
	for service, id := range na.Services {
		s[service] = id
	}
	na.Services = s

	na.Metrics = na.Metrics.Copy()

	ts := make(map[string]*TaskState, len(na.TaskStates))
	for task, state := range na.TaskStates {
		ts[task] = state.Copy()
	}
	na.TaskStates = ts
	return na
}

// TerminalStatus returns if the desired or actual status is terminal and
// will no longer transition.
func (a *Allocation) TerminalStatus() bool {
	// First check the desired state and if that isn't terminal, check client
	// state.
	switch a.DesiredStatus {
	case AllocDesiredStatusStop, AllocDesiredStatusEvict, AllocDesiredStatusFailed:
		return true
	default:
	}

	switch a.ClientStatus {
	case AllocClientStatusDead, AllocClientStatusFailed:
		return true
	default:
		return false
	}
}

// Stub returns a list stub for the allocation
func (a *Allocation) Stub() *AllocListStub {
	return &AllocListStub{
		ID:                 a.ID,
		EvalID:             a.EvalID,
		Name:               a.Name,
		NodeID:             a.NodeID,
		JobID:              a.JobID,
		TaskGroup:          a.TaskGroup,
		DesiredStatus:      a.DesiredStatus,
		DesiredDescription: a.DesiredDescription,
		ClientStatus:       a.ClientStatus,
		ClientDescription:  a.ClientDescription,
		TaskStates:         a.TaskStates,
		CreateIndex:        a.CreateIndex,
		ModifyIndex:        a.ModifyIndex,
		CreateTime:         a.CreateTime,
	}
}

// PopulateServiceIDs generates the service IDs for all the service definitions
// in that Allocation
func (a *Allocation) PopulateServiceIDs(tg *TaskGroup) {
	// Retain the old services, and re-initialize. We may be removing
	// services, so we cannot update the existing map.
	previous := a.Services
	a.Services = make(map[string]string)

	for _, task := range tg.Tasks {
		for _, service := range task.Services {
			// Retain the service if an ID is already generated
			if id, ok := previous[service.Name]; ok {
				a.Services[service.Name] = id
				continue
			}

			// If the service hasn't been generated an ID, we generate one.
			// We add a prefix to the Service ID so that we can know that this service
			// is managed by Nomad since Consul can also have service which are not
			// managed by Nomad
			a.Services[service.Name] = fmt.Sprintf("%s-%s", NomadConsulPrefix, GenerateUUID())
		}
	}
}

// AllocListStub is used to return a subset of alloc information
type AllocListStub struct {
	ID                 string
	EvalID             string
	Name               string
	NodeID             string
	JobID              string
	TaskGroup          string
	DesiredStatus      string
	DesiredDescription string
	ClientStatus       string
	ClientDescription  string
	TaskStates         map[string]*TaskState
	CreateIndex        uint64
	ModifyIndex        uint64
	CreateTime         int64
}

// AllocMetric is used to track various metrics while attempting
// to make an allocation. These are used to debug a job, or to better
// understand the pressure within the system.
type AllocMetric struct {
	// NodesEvaluated is the number of nodes that were evaluated
	NodesEvaluated int

	// NodesFiltered is the number of nodes filtered due to a constraint
	NodesFiltered int

	// NodesAvailable is the number of nodes available for evaluation per DC.
	NodesAvailable map[string]int

	// ClassFiltered is the number of nodes filtered by class
	ClassFiltered map[string]int

	// ConstraintFiltered is the number of failures caused by constraint
	ConstraintFiltered map[string]int

	// NodesExhausted is the number of nodes skipped due to being
	// exhausted of at least one resource
	NodesExhausted int

	// ClassExhausted is the number of nodes exhausted by class
	ClassExhausted map[string]int

	// DimensionExhausted provides the count by dimension or reason
	DimensionExhausted map[string]int

	// Scores is the scores of the final few nodes remaining
	// for placement. The top score is typically selected.
	Scores map[string]float64

	// AllocationTime is a measure of how long the allocation
	// attempt took. This can affect performance and SLAs.
	AllocationTime time.Duration

	// CoalescedFailures indicates the number of other
	// allocations that were coalesced into this failed allocation.
	// This is to prevent creating many failed allocations for a
	// single task group.
	CoalescedFailures int
}

func (a *AllocMetric) Copy() *AllocMetric {
	if a == nil {
		return nil
	}
	na := new(AllocMetric)
	*na = *a
	na.NodesAvailable = CopyMapStringInt(na.NodesAvailable)
	na.ClassFiltered = CopyMapStringInt(na.ClassFiltered)
	na.ConstraintFiltered = CopyMapStringInt(na.ConstraintFiltered)
	na.ClassExhausted = CopyMapStringInt(na.ClassExhausted)
	na.DimensionExhausted = CopyMapStringInt(na.DimensionExhausted)
	na.Scores = CopyMapStringFloat64(na.Scores)
	return na
}

func (a *AllocMetric) EvaluateNode() {
	a.NodesEvaluated += 1
}

func (a *AllocMetric) FilterNode(node *Node, constraint string) {
	a.NodesFiltered += 1
	if node != nil && node.NodeClass != "" {
		if a.ClassFiltered == nil {
			a.ClassFiltered = make(map[string]int)
		}
		a.ClassFiltered[node.NodeClass] += 1
	}
	if constraint != "" {
		if a.ConstraintFiltered == nil {
			a.ConstraintFiltered = make(map[string]int)
		}
		a.ConstraintFiltered[constraint] += 1
	}
}

func (a *AllocMetric) ExhaustedNode(node *Node, dimension string) {
	a.NodesExhausted += 1
	if node != nil && node.NodeClass != "" {
		if a.ClassExhausted == nil {
			a.ClassExhausted = make(map[string]int)
		}
		a.ClassExhausted[node.NodeClass] += 1
	}
	if dimension != "" {
		if a.DimensionExhausted == nil {
			a.DimensionExhausted = make(map[string]int)
		}
		a.DimensionExhausted[dimension] += 1
	}
}

func (a *AllocMetric) ScoreNode(node *Node, name string, score float64) {
	if a.Scores == nil {
		a.Scores = make(map[string]float64)
	}
	key := fmt.Sprintf("%s.%s", node.ID, name)
	a.Scores[key] = score
}

const (
	EvalStatusBlocked   = "blocked"
	EvalStatusPending   = "pending"
	EvalStatusComplete  = "complete"
	EvalStatusFailed    = "failed"
	EvalStatusCancelled = "canceled"
)

const (
	EvalTriggerJobRegister   = "job-register"
	EvalTriggerJobDeregister = "job-deregister"
	EvalTriggerPeriodicJob   = "periodic-job"
	EvalTriggerNodeUpdate    = "node-update"
	EvalTriggerScheduled     = "scheduled"
	EvalTriggerForceGC       = "force-gc"
	EvalTriggerRollingUpdate = "rolling-update"
)

const (
	// CoreJobEvalGC is used for the garbage collection of evaluations
	// and allocations. We periodically scan evaluations in a terminal state,
	// in which all the corresponding allocations are also terminal. We
	// delete these out of the system to bound the state.
	CoreJobEvalGC = "eval-gc"

	// CoreJobNodeGC is used for the garbage collection of failed nodes.
	// We periodically scan nodes in a terminal state, and if they have no
	// corresponding allocations we delete these out of the system.
	CoreJobNodeGC = "node-gc"

	// CoreJobJobGC is used for the garbage collection of eligible jobs. We
	// periodically scan garbage collectible jobs and check if both their
	// evaluations and allocations are terminal. If so, we delete these out of
	// the system.
	CoreJobJobGC = "job-gc"
)

// Evaluation is used anytime we need to apply business logic as a result
// of a change to our desired state (job specification) or the emergent state
// (registered nodes). When the inputs change, we need to "evaluate" them,
// potentially taking action (allocation of work) or doing nothing if the state
// of the world does not require it.
type Evaluation struct {
	// ID is a randonly generated UUID used for this evaluation. This
	// is assigned upon the creation of the evaluation.
	ID string

	// Priority is used to control scheduling importance and if this job
	// can preempt other jobs.
	Priority int

	// Type is used to control which schedulers are available to handle
	// this evaluation.
	Type string

	// TriggeredBy is used to give some insight into why this Eval
	// was created. (Job change, node failure, alloc failure, etc).
	TriggeredBy string

	// JobID is the job this evaluation is scoped to. Evaluations cannot
	// be run in parallel for a given JobID, so we serialize on this.
	JobID string

	// JobModifyIndex is the modify index of the job at the time
	// the evaluation was created
	JobModifyIndex uint64

	// NodeID is the node that was affected triggering the evaluation.
	NodeID string

	// NodeModifyIndex is the modify index of the node at the time
	// the evaluation was created
	NodeModifyIndex uint64

	// Status of the evaluation
	Status string

	// StatusDescription is meant to provide more human useful information
	StatusDescription string

	// Wait is a minimum wait time for running the eval. This is used to
	// support a rolling upgrade.
	Wait time.Duration

	// NextEval is the evaluation ID for the eval created to do a followup.
	// This is used to support rolling upgrades, where we need a chain of evaluations.
	NextEval string

	// PreviousEval is the evaluation ID for the eval creating this one to do a followup.
	// This is used to support rolling upgrades, where we need a chain of evaluations.
	PreviousEval string

	// ClassEligibility tracks computed node classes that have been explicitely
	// marked as eligible or ineligible.
	ClassEligibility map[string]bool

	// EscapedComputedClass marks whether the job has constraints that are not
	// captured by computed node classes.
	EscapedComputedClass bool

	// Raft Indexes
	CreateIndex uint64
	ModifyIndex uint64
}

// TerminalStatus returns if the current status is terminal and
// will no longer transition.
func (e *Evaluation) TerminalStatus() bool {
	switch e.Status {
	case EvalStatusComplete, EvalStatusFailed, EvalStatusCancelled:
		return true
	default:
		return false
	}
}

func (e *Evaluation) GoString() string {
	return fmt.Sprintf("<Eval '%s' JobID: '%s'>", e.ID, e.JobID)
}

func (e *Evaluation) Copy() *Evaluation {
	if e == nil {
		return nil
	}
	ne := new(Evaluation)
	*ne = *e
	return ne
}

// ShouldEnqueue checks if a given evaluation should be enqueued into the
// eval_broker
func (e *Evaluation) ShouldEnqueue() bool {
	switch e.Status {
	case EvalStatusPending:
		return true
	case EvalStatusComplete, EvalStatusFailed, EvalStatusBlocked, EvalStatusCancelled:
		return false
	default:
		panic(fmt.Sprintf("unhandled evaluation (%s) status %s", e.ID, e.Status))
	}
}

// ShouldBlock checks if a given evaluation should be entered into the blocked
// eval tracker.
func (e *Evaluation) ShouldBlock() bool {
	switch e.Status {
	case EvalStatusBlocked:
		return true
	case EvalStatusComplete, EvalStatusFailed, EvalStatusPending, EvalStatusCancelled:
		return false
	default:
		panic(fmt.Sprintf("unhandled evaluation (%s) status %s", e.ID, e.Status))
	}
}

// MakePlan is used to make a plan from the given evaluation
// for a given Job
func (e *Evaluation) MakePlan(j *Job) *Plan {
	p := &Plan{
		EvalID:         e.ID,
		Priority:       e.Priority,
		Job:            j,
		NodeUpdate:     make(map[string][]*Allocation),
		NodeAllocation: make(map[string][]*Allocation),
	}
	if j != nil {
		p.AllAtOnce = j.AllAtOnce
	}
	return p
}

// NextRollingEval creates an evaluation to followup this eval for rolling updates
func (e *Evaluation) NextRollingEval(wait time.Duration) *Evaluation {
	return &Evaluation{
		ID:             GenerateUUID(),
		Priority:       e.Priority,
		Type:           e.Type,
		TriggeredBy:    EvalTriggerRollingUpdate,
		JobID:          e.JobID,
		JobModifyIndex: e.JobModifyIndex,
		Status:         EvalStatusPending,
		Wait:           wait,
		PreviousEval:   e.ID,
	}
}

// BlockedEval creates a blocked evaluation to followup this eval to place any
// failed allocations. It takes the classes marked explicitely eligible or
// ineligible and whether the job has escaped computed node classes.
func (e *Evaluation) BlockedEval(classEligibility map[string]bool, escaped bool) *Evaluation {
	return &Evaluation{
		ID:                   GenerateUUID(),
		Priority:             e.Priority,
		Type:                 e.Type,
		TriggeredBy:          e.TriggeredBy,
		JobID:                e.JobID,
		JobModifyIndex:       e.JobModifyIndex,
		Status:               EvalStatusBlocked,
		PreviousEval:         e.ID,
		ClassEligibility:     classEligibility,
		EscapedComputedClass: escaped,
	}
}

// Plan is used to submit a commit plan for task allocations. These
// are submitted to the leader which verifies that resources have
// not been overcommitted before admiting the plan.
type Plan struct {
	// EvalID is the evaluation ID this plan is associated with
	EvalID string

	// EvalToken is used to prevent a split-brain processing of
	// an evaluation. There should only be a single scheduler running
	// an Eval at a time, but this could be violated after a leadership
	// transition. This unique token is used to reject plans that are
	// being submitted from a different leader.
	EvalToken string

	// Priority is the priority of the upstream job
	Priority int

	// AllAtOnce is used to control if incremental scheduling of task groups
	// is allowed or if we must do a gang scheduling of the entire job.
	// If this is false, a plan may be partially applied. Otherwise, the
	// entire plan must be able to make progress.
	AllAtOnce bool

	// Job is the parent job of all the allocations in the Plan.
	// Since a Plan only involves a single Job, we can reduce the size
	// of the plan by only including it once.
	Job *Job

	// NodeUpdate contains all the allocations for each node. For each node,
	// this is a list of the allocations to update to either stop or evict.
	NodeUpdate map[string][]*Allocation

	// NodeAllocation contains all the allocations for each node.
	// The evicts must be considered prior to the allocations.
	NodeAllocation map[string][]*Allocation

	// FailedAllocs are allocations that could not be made,
	// but are persisted so that the user can use the feedback
	// to determine the cause.
	FailedAllocs []*Allocation
}

func (p *Plan) AppendUpdate(alloc *Allocation, status, desc string) {
	newAlloc := new(Allocation)
	*newAlloc = *alloc

	// If the job is not set in the plan we are deregistering a job so we
	// extract the job from the allocation.
	if p.Job == nil && newAlloc.Job != nil {
		p.Job = newAlloc.Job
	}

	// Normalize the job
	newAlloc.Job = nil

	// Strip the resources as it can be rebuilt.
	newAlloc.Resources = nil

	newAlloc.DesiredStatus = status
	newAlloc.DesiredDescription = desc
	node := alloc.NodeID
	existing := p.NodeUpdate[node]
	p.NodeUpdate[node] = append(existing, newAlloc)
}

func (p *Plan) PopUpdate(alloc *Allocation) {
	existing := p.NodeUpdate[alloc.NodeID]
	n := len(existing)
	if n > 0 && existing[n-1].ID == alloc.ID {
		existing = existing[:n-1]
		if len(existing) > 0 {
			p.NodeUpdate[alloc.NodeID] = existing
		} else {
			delete(p.NodeUpdate, alloc.NodeID)
		}
	}
}

func (p *Plan) AppendAlloc(alloc *Allocation) {
	node := alloc.NodeID
	existing := p.NodeAllocation[node]
	p.NodeAllocation[node] = append(existing, alloc)
}

func (p *Plan) AppendFailed(alloc *Allocation) {
	p.FailedAllocs = append(p.FailedAllocs, alloc)
}

// IsNoOp checks if this plan would do nothing
func (p *Plan) IsNoOp() bool {
	return len(p.NodeUpdate) == 0 && len(p.NodeAllocation) == 0 && len(p.FailedAllocs) == 0
}

// PlanResult is the result of a plan submitted to the leader.
type PlanResult struct {
	// NodeUpdate contains all the updates that were committed.
	NodeUpdate map[string][]*Allocation

	// NodeAllocation contains all the allocations that were committed.
	NodeAllocation map[string][]*Allocation

	// FailedAllocs are allocations that could not be made,
	// but are persisted so that the user can use the feedback
	// to determine the cause.
	FailedAllocs []*Allocation

	// RefreshIndex is the index the worker should refresh state up to.
	// This allows all evictions and allocations to be materialized.
	// If any allocations were rejected due to stale data (node state,
	// over committed) this can be used to force a worker refresh.
	RefreshIndex uint64

	// AllocIndex is the Raft index in which the evictions and
	// allocations took place. This is used for the write index.
	AllocIndex uint64
}

// IsNoOp checks if this plan result would do nothing
func (p *PlanResult) IsNoOp() bool {
	return len(p.NodeUpdate) == 0 && len(p.NodeAllocation) == 0 && len(p.FailedAllocs) == 0
}

// FullCommit is used to check if all the allocations in a plan
// were committed as part of the result. Returns if there was
// a match, and the number of expected and actual allocations.
func (p *PlanResult) FullCommit(plan *Plan) (bool, int, int) {
	expected := 0
	actual := 0
	for name, allocList := range plan.NodeAllocation {
		didAlloc, _ := p.NodeAllocation[name]
		expected += len(allocList)
		actual += len(didAlloc)
	}
	return actual == expected, expected, actual
}

// msgpackHandle is a shared handle for encoding/decoding of structs
var MsgpackHandle = func() *codec.MsgpackHandle {
	h := &codec.MsgpackHandle{RawToString: true}

	// Sets the default type for decoding a map into a nil interface{}.
	// This is necessary in particular because we store the driver configs as a
	// nil interface{}.
	h.MapType = reflect.TypeOf(map[string]interface{}(nil))
	return h
}()

var HashiMsgpackHandle = func() *hcodec.MsgpackHandle {
	h := &hcodec.MsgpackHandle{RawToString: true}

	// Sets the default type for decoding a map into a nil interface{}.
	// This is necessary in particular because we store the driver configs as a
	// nil interface{}.
	h.MapType = reflect.TypeOf(map[string]interface{}(nil))
	return h
}()

// Decode is used to decode a MsgPack encoded object
func Decode(buf []byte, out interface{}) error {
	return codec.NewDecoder(bytes.NewReader(buf), MsgpackHandle).Decode(out)
}

// Encode is used to encode a MsgPack object with type prefix
func Encode(t MessageType, msg interface{}) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(uint8(t))
	err := codec.NewEncoder(&buf, MsgpackHandle).Encode(msg)
	return buf.Bytes(), err
}
