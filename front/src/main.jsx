// React 应用入口：将 App 挂载到 #root DOM 节点
import React from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.jsx'

createRoot(document.getElementById('root')).render(<App />)