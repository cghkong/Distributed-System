# Distributed-System

## Lab1  MapReduce
实现一个分布式MapReduce计算模型，主要逻辑分为协调器核工作器

### worker
worker的逻辑实现主要是一个for循环，不断地请求任务然后执行相应的任务，直到所有任务都做完才可退出。
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



### Coordinator


## Lab2  Raft
