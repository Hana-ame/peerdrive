import { useState, useCallback, useRef } from 'react'
import { getTaskStatus } from '../api/client'

export function useTask() {
  const [taskStatus, setTaskStatus] = useState<string>('')
  const intervalRef = useRef<ReturnType<typeof setInterval>>(undefined)

  const poll = useCallback((taskId: number) => {
    if (intervalRef.current) clearInterval(intervalRef.current)
    intervalRef.current = setInterval(async () => {
      try {
        const res = await getTaskStatus(taskId)
        setTaskStatus(res.task.status)
        if (res.task.status === 'completed' || res.task.status === 'failed') {
          if (intervalRef.current) clearInterval(intervalRef.current)
        }
      } catch {
        setTaskStatus('error')
        if (intervalRef.current) clearInterval(intervalRef.current)
      }
    }, 2000)
  }, [])

  return { taskStatus, poll }
}
