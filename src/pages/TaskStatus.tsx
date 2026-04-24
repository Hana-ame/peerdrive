import { useState } from 'react'
import { getTaskStatus } from '../api/client'
import toast from 'react-hot-toast'

export default function TaskStatus() {
  const [taskId, setTaskId] = useState('')
  const [task, setTask] = useState<any>(null)

  const handleQuery = async () => {
    if (!taskId) return
    try {
      const r = await getTaskStatus(Number(taskId))
      setTask(r.task)
    } catch (e: any) {
      toast.error(e.message)
    }
  }

  return (
    <div className="p-4">
      <h1 className="text-2xl font-bold mb-4">Task Status</h1>
      <div className="flex gap-2 mb-4">
        <input
          value={taskId}
          onChange={e => setTaskId(e.target.value)}
          placeholder="Task ID"
          className="border p-2 rounded"
        />
        <button onClick={handleQuery} className="bg-blue-600 text-white px-4 py-2 rounded">
          Query
        </button>
      </div>
      {task && (
        <pre className="bg-gray-100 p-4 rounded text-xs">{JSON.stringify(task, null, 2)}</pre>
      )}
    </div>
  )
}
