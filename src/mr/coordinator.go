package mr

import (
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Coordinator struct {
	// protect coordinate state the concurrent access
	mu           sync.Mutex
	mapFiles     []string
	nMapTasks    int
	NReduceTasks int

	// keep back track of when tasks are assigned and which tasks hava finished
	mapTasksFinished    []bool
	mapTasksIssued      []time.Time
	reduceTasksFinished []bool
	reduceTasksIssued   []time.Time

	// set to true when all reduce tasks are complete
	isDone bool
}

// Your code here -- RPC handlers for the worker to call.

// Handle GetTasks rpcs from worker

func (c *Coordinator) HandleGetTask(args *GetTaskArgs, reply *GetTaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	reply.NReducetasks = c.NReduceTasks
	reply.NMaptasks = c.nMapTasks

	// issue all map tasks until there are no map tasks left
	for {
		mapDone := true
		for m, done := range c.mapTasksFinished {
			if !done {
				// assign a task if it's either never been issued, or if it's been too long
				// since it was issued so the workor may have crashed
				// Note: if task has never been issued, time is initalized to 0 UTC
				if c.mapTasksIssued[m].IsZero() ||
					time.Since(c.mapTasksIssued[m]).Seconds() > 10 {
					reply.TaskType = Map
					reply.Tasknum = m
					reply.Mapfile = c.mapFiles[m]
					c.mapTasksIssued[m] = time.Now()
					return nil
				} else {
					mapDone = false
				}
			}
		}
		// if all maps are in progress and haven't timed out,wait to give another task

		if !mapDone {
			// TODO wait
			time.Sleep(2 * time.Second)
		} else {
			// we are been done all map tasks! yeah
			break
		}

	}

	// all map task are done , issue reduce tasks now
	for {
		redDone := true
		for r, done := range c.reduceTasksFinished {
			if !done {
				// assign a task if it is either never been issued , or if it is been too long
				// since it was issued so the worker may have crashed
				// Note: if task has never been issued, time is initalized to 0 UTC
				if c.reduceTasksIssued[r].IsZero() ||
					time.Since(c.reduceTasksIssued[r]).Seconds() > 10 {
					reply.TaskType = Reduce
					reply.Tasknum = r
					c.reduceTasksIssued[r] = time.Now()
					return nil
				} else {
					redDone = false
				}
			}
		}

		// if all reduces are in progress and haven't timed out, wait to give another task
		if !redDone {
			// wait
			time.Sleep(2 * time.Second)
		} else {
			// we are done all the reduce tasks
			break
		}

	}
	// if all map and reduce tasks are done, send the querying worker
	// a Done TaskType, and set isDone to true

	reply.TaskType = Done
	c.isDone = true

	return nil
}

func (c *Coordinator) HandleFinishedTask(args *FinishedTaskArgs, reply *FinishedTaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch args.TaskType {
	case Map:
		c.mapTasksFinished[args.Tasknum] = true
	case Reduce:
		c.reduceTasksFinished[args.Tasknum] = true
	default:
		log.Fatalf("Bad finished task? %s", args.TaskType)
	}

	return nil
}

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	ret := false

	// Your code here.
	c.mu.Lock()
	ret = c.isDone
	c.mu.Unlock()

	return ret
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	NMap := len(files)
	c := Coordinator{
		mapFiles:            files,
		nMapTasks:           NMap,
		NReduceTasks:        nReduce,
		mapTasksFinished:    make([]bool, NMap),
		mapTasksIssued:      make([]time.Time, NMap),
		reduceTasksFinished: make([]bool, nReduce),
		reduceTasksIssued:   make([]time.Time, nReduce),
		isDone:              false,
	}

	// Your code here.
	const TempDir = "tmp"

	outFiles, _ := filepath.Glob("mr-out*")
	for _, f := range outFiles {
		if err := os.Remove(f); err != nil {
			log.Fatalf("Cannot remove file %v\n", f)
		}
	}
	err := os.RemoveAll(TempDir)
	if err != nil {
		log.Fatalf("Cannot remove temp directory %v\n", TempDir)
	}
	err = os.Mkdir(TempDir, 0755)
	if err != nil {
		log.Fatalf("Cannot create temp directory %v\n", TempDir)
	}

	c.server()
	return &c
}
