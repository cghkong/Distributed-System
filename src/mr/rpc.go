package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import (
	"os"
	"strconv"
)

//
// example to show how to declare the arguments
// and reply for an RPC.
//

type ExampleArgs struct {
	X int
}

type ExampleReply struct {
	Y int
}

// Add your RPC definitions here.
// define the const type for the task type
type TaskType int

const (
	Map    TaskType = 1
	Reduce TaskType = 2
	Done   TaskType = 3 // there are no pending tasks
)

/**
Get Task rpcs are sent from an idle worker to coordinator to ask for the next task
*/

// don't need any args
type GetTaskArgs struct{}

type GetTaskReply struct {
	//to indicate which type of task
	TaskType TaskType

	// task number of either map or reduce task
	Tasknum int

	// for Map (to know which file to write)
	NReducetasks int

	// for Map ( to know which file to read)
	Mapfile string

	// for reduce (to know how many intermediate map files to read)
	NMaptasks int
}

/**
FinishedTask rpcs are sent from an idle worker to coordinator to indicate that a  task has been completed
*/

type FinishedTaskArgs struct {
	// what type of task was the worker assigned
	TaskType TaskType
	Tasknum  int
}

// workers don't need to get a reply
type FinishedTaskReply struct{}

// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/824-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
