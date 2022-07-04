package mr

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"

	"io/ioutil"
	"log"
	"net/rpc"
	"os"
	"sort"
)

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// for sorting by key
type ByKey []KeyValue

func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

//atomically renames final reduced file

func finalizeReduceFile(tmpFile string, taskN int) {
	finalFile := fmt.Sprintf("mr-out-%d", taskN)
	os.Rename(tmpFile, finalFile)
}

//get name of the intermediate file, given the map and reduce task numbers

func getIntermediateFile(mapTaskN int, redTaskN int) string {
	//  log.Printf("Getting intermediate file for map %d and red %d\n",mapTaskN,redTaskN)
	return fmt.Sprintf("tmp/mr-%d-%d", mapTaskN, redTaskN)
}

//
// atomically rename temporary intermediate files to a completed intermediate task file
//
func finalizeIntermediateFile(tmpFile string, mapTaskN int, redTaskN int) {
	finalFile := getIntermediateFile(mapTaskN, redTaskN)
	os.Rename(tmpFile, finalFile)
}

// implementation of map task
func performMap(filename string, TaskNum int, nReduceTasks int, mapf func(string, string) []KeyValue) {
	// read contents to map
	file, err := os.Open(filename)
	if err != nil {
		log.Fatalf("cannot open %v", filename)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", filename)
	}
	file.Close()

	// apply map function to contents of file and collect
	// the set of key-value pairs
	kva := mapf(filename, string(content))

	// create temporary files and encoders for each file
	tmpFiles := []*os.File{}
	tmpFilenames := []string{}

	encoders := []*json.Encoder{}
	buffers := make([]*bufio.Writer, 0, nReduceTasks)

	for r := 0; r < nReduceTasks; r++ {
		file, err := os.Create(fmt.Sprintf("tmp/mr-%d", os.Getpid()))
		if err != nil {
			fmt.Printf("create tmp file error:%T", err)
		}

		tmpFiles = append(tmpFiles, file)
		tmpFilenames = append(tmpFilenames, file.Name())
		buf := bufio.NewWriter(file)
		buffers = append(buffers, buf)
		enc := json.NewEncoder(buf)
		encoders = append(encoders, enc)
	}
	// write output keys to approciate(tempeorary) intermediate files using the provided ihash function

	for _, kv := range kva {
		r := ihash(kv.Key) % nReduceTasks
		err := encoders[r].Encode(&kv)
		if err != nil {
			log.Fatalf("encode error %T   file kv %v\n", err, kv)
		}
	}

	for _, buf := range buffers {
		err := buf.Flush()
		if err != nil {
			log.Fatalf("Cannot flush buffer for file\n")
		}
	}

	for _, f := range tmpFiles {
		// print("tmp file %s\n", f)
		f.Close()
	}

	// atomically  rename temp files to final intermediate files
	for r := 0; r < nReduceTasks; r++ {
		finalizeIntermediateFile(tmpFilenames[r], TaskNum, r)
	}
}

// implementation of reduce task
func performReduce(taskNum int, nMapTasks int, reducef func(string, []string) string) {
	// get all intermediate files corresponding to this reduce task, and collect the corresponding key-value pairs

	kva := []KeyValue{}
	for m := 0; m < nMapTasks; m++ {
		iFilename := getIntermediateFile(m, taskNum)
		file, err := os.Open(iFilename)
		// fmt.Printf("perform reduce error file %v\n", iFilename)
		// fmt.Printf("error type %T\n", err)
		if err != nil {
			log.Fatalf("cannot open %v", iFilename)
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kva = append(kva, kv)
		}
		file.Close()
	}

	// sort the keys
	sort.Sort(ByKey(kva))
	// get temporary reduce file to write values
	//tmpFile, err := ioutil.TempFile("", "")
	tmpFile, err := os.Create(fmt.Sprintf("tmp/mr-out-%d", taskNum))
	if err != nil {
		log.Fatalf("cannot open tmpfile")
	}
	tmpFilename := tmpFile.Name()
	// apply reduce function once to all values of the same key
	key_begin := 0
	for key_begin < len(kva) {
		key_end := key_begin + 1

		// this loop find all the values with the same key
		for key_end < len(kva) && kva[key_end].Key == kva[key_begin].Key {
			key_end++
		}

		values := []string{}
		for k := key_begin; k < key_end; k++ {
			values = append(values, kva[k].Value)
		}
		output := reducef(kva[key_begin].Key, values)

		fmt.Fprintf(tmpFile, "%v %v\n", kva[key_begin].Key, output)

		// go to next key
		key_begin = key_end
	}

	tmpFile.Close()

	// atomically rename reduce file to final reduce file
	finalizeReduceFile(tmpFilename, taskNum)

}

//
// main/mrworker.go calls this function.
//
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.

	// uncomment to send the Example RPC to the coordinator.
	// CallExample()
	for {
		args := GetTaskArgs{}
		reply := GetTaskReply{}
		// this will wait until we get assigned a task
		call("Coordinator.HandleGetTask", &args, &reply)

		switch reply.TaskType {
		case Map:
			performMap(reply.Mapfile, reply.Tasknum, reply.NReducetasks, mapf)
		case Reduce:
			performReduce(reply.Tasknum, reply.NMaptasks, reducef)
		case Done:
			// there are no tasks remaining let's exit
			os.Exit(0)
		default:
			fmt.Errorf("Bad task type? %s", reply.TaskType)
		}

		// tell coordinator that we are done
		finargs := FinishedTaskArgs{
			TaskType: reply.TaskType,
			Tasknum:  reply.Tasknum,
		}

		finreply := FinishedTaskReply{}
		call("Coordinator.HandleFinishedTask", &finargs, &finreply)

	}
}

//
// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
//
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
