# Distributed-System

## Lab1  MapReduce
实现一个分布式MapReduce计算模型，主要逻辑分为协调器(coordinator)和工作器(worker)

### worker
worker的逻辑实现主要是一个for循环，不断地请求任务然后执行相应的任务，执行完一个任务之后会通知coordinator做相应的处理。
当所有的Map任务和Reduce任务都做完之后，退出程序。
```
for {
		args := GetTaskArgs{}
		reply := GetTaskReply{}
		// request for task
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

		//inform coordintor that worker haved complete task
		finargs := FinishedTaskArgs{
			TaskType: reply.TaskType,
			Tasknum:  reply.Tasknum,
		}

		finreply := FinishedTaskReply{}
		call("Coordinator.HandleFinishedTask", &finargs, &finreply)
	}
```
#### DoMap实现逻辑
打开将要处理的任务文件，使用客户端给定的mapf函数处理，返回KV键值对，然后创建临时文件(**特别注意，直接创建文件会有bug**)，根据给定的hash函数将kv键值对保存在临时文件中，最后原子操作重命名文件
```
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
		file, err := os.CreateTemp("tmp", "map-")
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

```
#### DoReduce
打开Map创建的临时任务，使用客户端给定的reducef处理KV键值对，保存到临时文件，最后重命名为最终的结果文件
```
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
	tmpFile, err := os.CreateTemp("tmp", "tmp-mr")
	
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

		key_begin = key_end
	}

	tmpFile.Close()

	// atomically rename reduce file to final reduce file
	finalizeReduceFile(tmpFilename, taskNum)
```

### RPC
主要设计RPC的请求参数和响应参数
```
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
```

### Coordinator
协调器的处理逻辑可以理解为是对请求任务的worker分配任务，分为Map和Reduce两个阶段，需要先做完Map，才能做Reduce任务。同时需要回应处理已经完成Map任务的worker。
```
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
			time.Sleep(time.Second)
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
			time.Sleep(time.Second)
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
```

#### 注意点：
1. worker超时执行会导致coordinator认为worker执行失败，重置该任务，然后发布给其它的worker执行

## Lab2  Raft

