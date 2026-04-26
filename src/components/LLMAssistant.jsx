import React, { useState, useRef, useEffect, useContext } from 'react';
import { useLocation } from 'react-router-dom';
import { PageContext } from '../App';
import { getLlmEndpoint, getLlmModel, getLlmApiKey, getLlmBodyTemplate } from '../api';

const SYSTEM_PROMPT = `You are an AI assistant integrated into PeerDrive, a decentralized file collection management platform.

You can help users:
1. Navigate and understand the application
2. Manage file collections (create, search, fork, merge, commit)
3. Work with file operations (upload, download, verify SHA256 hashes)
4. Configure P2P networking and node settings
5. Create and manage anonymous collections via drag-and-drop
6. Register local files and folders for tracking

Current page state will be provided below as JSON context. Use this to give relevant, contextual answers about what the user is seeing.
Respond in the same language the user uses. Be concise and helpful.`;

export default function LLMAssistant() {
  const [open, setOpen] = useState(false);
  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [streamText, setStreamText] = useState('');
  const chatRef = useRef(null);
  const abortRef = useRef(null);
  const location = useLocation();
  const { pageContext } = useContext(PageContext) || {};

  useEffect(() => {
    if (chatRef.current) {
      chatRef.current.scrollTop = chatRef.current.scrollHeight;
    }
  }, [messages, streamText]);

  const buildContext = () => ({
    route: location.pathname,
    timestamp: new Date().toISOString(),
    pageData: pageContext || null,
  });

  const handleStopStream = () => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    if (streamText) {
      setMessages(prev => [...prev, { role: 'assistant', content: streamText }]);
      setStreamText('');
    }
    setStreaming(false);
  };

  const handleSend = async () => {
    if (!input.trim() || streaming) return;
    const userMsg = { role: 'user', content: input };
    const newMessages = [...messages, userMsg];
    setMessages(newMessages);
    setInput('');
    setStreaming(true);
    setStreamText('');

    const endpoint = getLlmEndpoint();
    const model = getLlmModel();
    const apiKey = getLlmApiKey();

    let body;
    try {
      body = JSON.parse(getLlmBodyTemplate());
    } catch {
      body = { model, messages: [], stream: true };
    }

    body.model = model || body.model;
    body.messages = [
      { role: 'system', content: SYSTEM_PROMPT + '\n\nCurrent page context:\n```json\n' + JSON.stringify(buildContext(), null, 2) + '\n```' },
      ...newMessages,
    ];

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const headers = { 'Content-Type': 'application/json' };
      if (apiKey) headers['Authorization'] = `Bearer ${apiKey}`;

      const res = await fetch(`${endpoint}/v1/chat/completions`, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal: controller.signal,
      });

      if (!res.ok) {
        const errText = await res.text().catch(() => `HTTP ${res.status}`);
        throw new Error(errText);
      }

      if (body.stream !== false) {
        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let fullText = '';
        let buffer = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() || '';
          for (const line of lines) {
            if (!line.startsWith('data: ')) continue;
            const data = line.slice(6);
            if (data === '[DONE]') continue;
            try {
              const json = JSON.parse(data);
              const content = json.choices?.[0]?.delta?.content || '';
              fullText += content;
              setStreamText(fullText);
            } catch {}
          }
        }
        setMessages([...newMessages, { role: 'assistant', content: fullText }]);
        setStreamText('');
      } else {
        const json = await res.json();
        const content = json.choices?.[0]?.message?.content || '';
        setMessages([...newMessages, { role: 'assistant', content }]);
      }
    } catch (e) {
      if (e.name !== 'AbortError') {
        setMessages([...newMessages, { role: 'assistant', content: `❌ ${e.message}` }]);
      }
    }
    setStreaming(false);
    abortRef.current = null;
  };

  return (
    <>
      {!open && (
        <button
          onClick={() => setOpen(true)}
          className="fixed bottom-6 right-6 w-14 h-14 bg-blue-600 hover:bg-blue-700 rounded-full shadow-lg flex items-center justify-center text-2xl z-40 transition-transform hover:scale-110"
          title="AI 助手"
        >
          🤖
        </button>
      )}
      {open && (
        <div className="fixed bottom-6 right-6 w-96 h-[560px] max-h-[75vh] bg-gray-900 border border-gray-700 rounded-xl shadow-2xl flex flex-col z-40">
          <div className="flex items-center justify-between px-4 py-3 border-b border-gray-700 shrink-0">
            <div className="flex items-center gap-2">
              <span className="text-lg">🤖</span>
              <span className="font-bold text-sm">AI 助手</span>
              <span className="text-[10px] text-gray-500">{getLlmModel()}</span>
            </div>
            <button onClick={() => setOpen(false)} className="text-gray-400 hover:text-white text-xl leading-none">&times;</button>
          </div>

          <div ref={chatRef} className="flex-1 overflow-y-auto p-4 space-y-3">
            {messages.length === 0 && (
              <div className="text-center pt-10">
                <p className="text-gray-500 text-sm">你好！我是 PeerDrive 的 AI 助手。</p>
                <p className="text-gray-600 text-xs mt-1">我能看到你当前页面的状态，可以帮你解答问题。</p>
              </div>
            )}
            {messages.map((m, i) => (
              <div key={i} className={`flex ${m.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                <div className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${
                  m.role === 'user'
                    ? 'bg-blue-600 text-white'
                    : 'bg-gray-800 text-gray-200 border border-gray-700'
                }`}>
                  <pre className="whitespace-pre-wrap font-sans break-words">{m.content}</pre>
                </div>
              </div>
            ))}
            {streamText && (
              <div className="flex justify-start">
                <div className="max-w-[85%] rounded-lg px-3 py-2 text-sm bg-gray-800 text-gray-200 border border-gray-700">
                  <pre className="whitespace-pre-wrap font-sans break-words">{streamText}<span className="animate-pulse">▊</span></pre>
                </div>
              </div>
            )}
          </div>

          <div className="p-3 border-t border-gray-700 shrink-0">
            <div className="flex gap-2">
              <input
                value={input}
                onChange={e => setInput(e.target.value)}
                onKeyDown={e => {
                  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
                }}
                placeholder={streaming ? 'AI 回复中...' : '输入消息...'}
                disabled={streaming}
                className="flex-1 bg-gray-800 border border-gray-600 rounded px-3 py-2 text-sm focus:outline-none focus:border-blue-500 disabled:opacity-50"
              />
              {streaming ? (
                <button
                  onClick={handleStopStream}
                  className="bg-red-600 hover:bg-red-700 px-4 py-2 rounded text-sm font-medium"
                >
                  停止
                </button>
              ) : (
                <button
                  onClick={handleSend}
                  disabled={!input.trim()}
                  className="bg-blue-600 hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed px-4 py-2 rounded text-sm font-medium"
                >
                  发送
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  );
}
