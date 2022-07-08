# Distributed-System（MIT 6.824）

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
			// wait
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
Raft是一种易于理解的共识算法，它可以产生和multi Paxos等价的效果。共识算法使得集群在有成员发送宕机的情况仍能提高可靠的服务，在大规模高可用的系统中具有重要意义。
Raft算法将问题划分为三个子问题（leader选举、日记复制、安全性）来解决。

**强烈建议**实现时参考MIT助教的指导说明https://thesquareplanet.com/blog/students-guide-to-raft/
和加锁建议https://pdos.csail.mit.edu/6.824/labs/raft-locking.txt

### Leader Election

#### Raft结构体设计
```
const (
	FOLLOWER     = 0
	CANDIDATE    = 1
	LEADER       = 2
}

 type Raft struct {
	mu                           sync.Mutex          // Lock to protect shared access to this peer's state
	peers                        []*labrpc.ClientEnd // RPC end points of all peers
	persister                    *Persister          // Object to hold this peer's persisted state
	me                           int                 // this peer's index into peers[]
	dead                         int32               // set by Kill()

	currentTerm                  int
	votedFor                     int
	getVoteNum                   int
	log                          []LogEntry

	commitIndex                  int
	lastApplied                  int

	state int
	lastResetElectionTime        time.Time

        nextIndex                    []int
	matchIndex                   []int

	applyCh                      chan ApplyMsg
	
	// used for snapshot
	lastSnapShotIndex            int
	lastSnapShotTerm             int
}

//LogEntry design
type Entry struct {
	Term    int
	Command interface{}
}
```

#### 2 Leader Election
##### 2.1 请求投票的参数和应答参数的结构体设计
```
type RequestVoteArgs struct {
	// Your data here (2A, 2B).
	Term int
	CandidateId int
	LastLogIndex int
	LastLogTerm int
}

type RequestVoteReply struct {
	// Your data here (2A).
	Term int
	VoteGranted bool
}
```

##### 2.2 leader选举算法实现
如果某个服务器结点的选举时间到期，则会重置选举超时时间，并且它会转化为候选者发起新的一轮选举并给自己投一票，然后并行地向其它结点发送投票请求。

2.2.1 如果其它结点的任期大于请求的任期，那么它不能获得那个结点的选票（因为日记一致性的需要）。

2.2.2 如果其它结点还没有投票并且候选者比本地日记新，则候选者可以获得该结点的选票

2.2.3 如果候选者获得的选票超过一半，则选举胜出，成为新的leader，向其它结点广播心跳

```
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (2A, 2B).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	
	if args.Term < rf.currentTerm {
		reply.VoteGranted = false
		reply.Term = rf.currentTerm
		return
	}

	reply.Term = rf.currentTerm

	if args.Term > rf.currentTerm {
		rf.currentTerm = args.Term
		rf.changeState(TO_FOLLOWER,false)
		rf.persist()
	}

	if rf.UpToDate(args.LastLogIndex,args.LastLogTerm) == false {
		reply.VoteGranted = false
		reply.Term = rf.currentTerm
		return
	}

	// arg.Term == rf.currentTerm
	if rf.votedFor != -1 && rf.votedFor != args.CandidateId && args.Term == reply.Term {
		reply.VoteGranted = false
		reply.Term = rf.currentTerm
		return
	}else {
		rf.votedFor = args.CandidateId
		reply.VoteGranted = true
		rf.currentTerm = args.Term
		reply.Term = rf.currentTerm
		rf.lastResetElectionTime = time.Now()
		rf.persist()
		return
	}
	return
}

func (rf *Raft) StartElection(){
	for index := range rf.peers{
		if index == rf.me{
			continue
		}

		go func(server int){
			rf.mu.Lock()
			rvArgs := RequestVoteArgs{
				rf.currentTerm,
				rf.me,
				rf.getLastIndex(),
				rf.getLastTerm(),
			}
			rvReply := RequestVoteReply{}
			rf.mu.Unlock()
			// waiting code should free lock first.
			re := rf.sendRequestVote(server,&rvArgs,&rvReply)
			if re == true {
				rf.mu.Lock()
			        // 抛弃过期的请求
				if rf.state != CANDIDATE || rvArgs.Term < rf.currentTerm{
					rf.mu.Unlock()
					return
				}

				if rvReply.VoteGranted == true && rf.currentTerm == rvArgs.Term{
					rf.getVoteNum += 1
					if rf.getVoteNum >= len(rf.peers)/2+1 {
						DPrintf("[LeaderSuccess+++++] %d got votenum: %d, needed >= %d, become leader for term %d",rf.me,rf.getVoteNum,len(rf.peers)/2+1,rf.currentTerm)
						rf.changeState(TO_LEADER,true)
						rf.mu.Unlock()
						return
					}
					rf.mu.Unlock()
					return
				}
                                // 其它结点率先选举胜出，成为leader
				if rvReply.Term > rvArgs.Term {
					if rf.currentTerm < rvReply.Term{
						rf.currentTerm = rvReply.Term
					}
					rf.changeState(TO_FOLLOWER,false)
					rf.mu.Unlock()
					return
				}

				rf.mu.Unlock()
				return
			}

		}(index)

	}

}

```
注意点：
1. 利用Go函数闭包来实现投票统计

2. 抛弃过期请求的恢复

3. 判断投票过程中是否有其它结点率先成为新的leader


#### 日记复制
日记复制是raft算法的难点，主要逻辑是leader定期向所有的Follower发送心跳和追加日记

##### 追加日记的逻辑实现（具体参考论文，引入snapshot之后会进一步完善）：

1. 如果某服务器结点的任期大于leader的任期，返回false（过期的请求直接抛弃）

2. 如果某服务器结点不存在和leader的前一条日记的索引和任期匹配的日记，返回false

3. 如果某服务器存在一条日记的索引和leader的前一条日记的索引相同，任期不同，则截断这条日记及之后的所有的日记（维护日记的一致性，没有被上一任期的leader提交并且持久化）

4. 如果leader将要复制的日记，本地服务器没有，则直接追加到存储

5. 如果leaderCommit > commitIndex，则将本地commitIndex = min (最新的日记的索引，leaderCommit)

发送心跳的目的：重置结点的选举超时时间
```
// 追加日记
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	// rule 1
	if args.Term < rf.currentTerm {
		reply.Term = rf.currentTerm
		reply.Success = false
		reply.ConflictingIndex = -1
		return
	}

	rf.currentTerm = args.Term
	
	reply.Term = rf.currentTerm
	reply.Success = true
	reply.ConflictingIndex = -1

	if rf.state!=FOLLOWER{
		rf.changeState(TO_FOLLOWER,true)
	}else{
		rf.lastResetElectionTime = time.Now()
		rf.persist()
	}
	
	if rf.lastSSPointIndex > args.PrevLogIndex{
		reply.Success = false
		reply.ConflictingIndex = rf.getLastIndex() + 1
		return
	}

	if rf.getLastIndex() < args.PrevLogIndex {
		reply.Success = false
		reply.ConflictingIndex = rf.getLastIndex()
		return
	} else {
		if rf.getLogTermWithIndex(args.PrevLogIndex) != args.PrevLogTerm{
			reply.Success = false
			tempTerm := rf.getLogTermWithIndex(args.PrevLogIndex)
			for index := args.PrevLogIndex;index >= rf.lastSSPointIndex;index--{
				if rf.getLogTermWithIndex(index) != tempTerm{
					reply.ConflictingIndex = index+1
					break
				}
			}
			return
		}
	}

	rf.log = append(rf.log[:args.PrevLogIndex+1-rf.lastSSPointIndex],args.Entries...)
	rf.persist()
	//rule 5
	if args.LeaderCommit > rf.commitIndex {
		rf.updateCommitIndex(FOLLOWER, args.LeaderCommit)
	}
	
	return
}


// 将心跳和追加日记放到一起实现
func (rf *Raft) LeaderAppendEntries(){
	// send to every server to replicate logs to them
	for index := range rf.peers{
		if index == rf.me {
			continue
		}
		// parallel replicate logs to sever

		go func(server int){
			rf.mu.Lock()
		        // 判断是否是leader调用
			if rf.state!=LEADER{
				rf.mu.Unlock()
				return
			}
			prevLogIndextemp := rf.nextIndex[server]-1
			// 日记压缩2D
			if prevLogIndextemp < rf.lastSSPointIndex{
				go rf.leaderSendSnapShot(server)
				rf.mu.Unlock()
				return
			}

			aeArgs := AppendEntriesArgs{}

			if rf.getLastIndex() >= rf.nextIndex[server] {
			        // 补全添加日记的内容
				entriesNeeded := make([]Entry,0)
				entriesNeeded = append(entriesNeeded,rf.log[rf.nextIndex[server]-rf.lastSSPointIndex:]...)
				prevLogIndex,prevLogTerm := rf.getPrevLogInfo(server)
				aeArgs = AppendEntriesArgs{
					rf.currentTerm,
					rf.me,
					prevLogIndex,
					prevLogTerm,
					entriesNeeded,
					rf.commitIndex,
				}
			}else {
			        // 没有添加日记内容，心跳
				prevLogIndex,prevLogTerm := rf.getPrevLogInfo(server)
				aeArgs = AppendEntriesArgs{
					rf.currentTerm,
					rf.me,
					prevLogIndex,
					prevLogTerm,
					[] Entry{},
					rf.commitIndex,
				}
			aeReply := AppendEntriesReply{}
			rf.mu.Unlock()

			re := rf.sendAppendEntries(server,&aeArgs,&aeReply)

			if re == true {
				rf.mu.Lock()
				defer rf.mu.Unlock()

				if rf.state!=LEADER{
					return
				}

				if aeReply.Term > rf.currentTerm {
					rf.currentTerm = aeReply.Term
					rf.changeState(TO_FOLLOWER,true)
					return
				}

				if aeReply.Success {
					rf.matchIndex[server] = aeArgs.PrevLogIndex + len(aeArgs.Entries)
					rf.nextIndex[server] = rf.matchIndex[server] + 1
					rf.updateCommitIndex(LEADER, 0)
				}

				if !aeReply.Success {
					if aeReply.ConflictingIndex!= -1 {
						rf.nextIndex[server] = aeReply.ConflictingIndex
					}
				}
			}

		}(index)

	}
}
```

##### leader 提交日记
一旦leader成功将一条日记复制到集群的大多数机器，那么这条日记就是已提交的（应用状态机）

实现机制：leader维护将要提交的日记记录的索引，并把这个这个索引放到最佳日记的请求中，其它服务结点会从请求中获得这个索引，然后在本地找到这条日记并应用到状态机（执行）。

解决措施：leader强制其它结点只能复制它的日记来解决不一致性，意味着发生冲突时，其它结点的日记会被删除或者重写

值得注意的是，在我们的框架，在给定一个任期内，leader创建的日记索引时单调递增且不重复的。



#### 持久化
persistence的作用希望在结点崩溃后可以读取并恢复自身状态，本次lab中需要持久化currentTerm、votedFor，log[]三个变量。可以简单理解为编码(序列化为字节数组)和解码的过程,使用框架提供的labgob实现。


#### 日记压缩
基本思路：使用快照替换之前已经提交的日记。leader使用快照复制给落后的Folower，Follower接受快照信息，删除这个快照并用新的快照取代，如果重复接受，可以删除快照之前的日记，当快照之后的日记需要保留。然而Follower可以在没有leader的情况下进行快照，只是Follower可以重组自己的数据而已，并不违背数据流向（leader  →  Follower）。

实现逻辑：
1. 如果leader的任期小于自己的任期，return false （抛弃过期的请求）

2. 创建快照文件，在快照文件中指定偏移量写入数据

3. 如果不是最后一个块文件，则等待更多的数据

4. 保存快照文件，删除快照之间的日记，快照之后的日记需要保留

5. 删除整个日记文件

6. 使用快照重置状态机，并加载快照



##### 注意点
1. 选举时间间隔的选取需要参考论文中的不等式，太小的选举超时间隔会导致选举次数增加，频繁的消耗RPC资源，不能通过B中的测试
   ![image](https://user-images.githubusercontent.com/79254572/178018554-bcaca818-0491-4b80-832c-90b142fa908a.png)
   
2. 注意参考按照TA中的要求加锁，所有涉及到rf.currentTerm或者rf.state都必须加锁，

3. 涉及到time.Sleep()的代码块不要加锁，否者容易导致死锁的发生，但可以在time.Sleep()之后加锁。












