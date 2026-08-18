// AI 助手聊天面板：集成 LLM，支持工具调用（导航/查询合集/注册文件等）
import React, { useState, useRef, useEffect, useContext } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { PageContext } from '../App';
import * as api from '../api';

const SYSTEM_PROMPT = `You are an AI assistant integrated into PeerDrive, a decentralized file collection management platform.

You can help users:
1. Navigate the application and understand what's on screen
2. Manage file collections (create, search, fork, merge, commit)
3. Work with file operations (upload, download, verify SHA256 hashes)
4. Configure P2P networking and node settings
5. Create and manage anonymous collections via drag-and-drop
6. Register local files and folders for tracking

You have access to tools that let you call PeerDrive APIs directly. When a user asks you to perform an action, use the appropriate tool. If a user asks to navigate somewhere, use navigate_to. If they ask about collections or want to modify data, use the corresponding tool.

When you execute tools, the results will be shown to you. Use them to compose a helpful final answer.

Current page state will be provided below as JSON context. Use this to give contextual answers.
Respond in the same language the user uses. Be concise and helpful.`;

const TOOLS = [
  {
    type: 'function',
    function: {
      name: 'navigate_to',
      description: 'Navigate the browser to another PeerDrive page. Use when the user wants to go to a different page.',
      parameters: {
        type: 'object',
        properties: {
          route: {
            type: 'string',
            description: 'Route to navigate to. Common routes: / (home), /files, /create, /p2p, /bt, /ipfs, /settings',
          },
        },
        required: ['route'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'get_node_info',
      description: 'Get the Peerdrive node connection status and basic info (ping + node info).',
      parameters: { type: 'object', properties: {}, required: [] },
    },
  },
  {
    type: 'function',
    function: {
      name: 'list_collections',
      description: 'List all collections. If username is provided, lists user collections; otherwise lists anonymous collections.',
      parameters: {
        type: 'object',
        properties: { username: { type: 'string', description: 'Optional username to list collections for' } },
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'list_public_collections',
      description: 'Browse publicly visible collections, optionally filtered by search query.',
      parameters: {
        type: 'object',
        properties: { q: { type: 'string', description: 'Optional search keyword' } },
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'search_collections',
      description: 'Search for collections by name across the network.',
      parameters: {
        type: 'object',
        properties: { q: { type: 'string', description: 'Search query' } },
        required: ['q'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'create_collection',
      description: 'Create a new user collection.',
      parameters: {
        type: 'object',
        properties: {
          username: { type: 'string', description: 'Username for the collection' },
          collection_name: { type: 'string', description: 'Name of the new collection' },
          visibility: { type: 'string', enum: ['public', 'unlisted', 'private'], description: 'Visibility setting' },
        },
        required: ['username', 'collection_name'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'get_collection_info',
      description: 'Get detailed information about a specific collection including its file entries.',
      parameters: {
        type: 'object',
        properties: {
          username: { type: 'string', description: 'Username who owns the collection' },
          collection_name: { type: 'string', description: 'Name of the collection' },
        },
        required: ['username', 'collection_name'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'add_file_to_collection',
      description: 'Add a file entry to an existing collection by its SHA256 hash.',
      parameters: {
        type: 'object',
        properties: {
          username: { type: 'string', description: 'Username who owns the collection' },
          collection_name: { type: 'string', description: 'Name of the collection' },
          path: { type: 'string', description: 'Virtual file path within the collection (e.g. docs/readme.txt)' },
          hash: { type: 'string', description: 'SHA256 hash of the file (64 hex chars)' },
        },
        required: ['username', 'collection_name', 'path', 'hash'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'commit_collection',
      description: 'Commit staged changes to a collection with a message.',
      parameters: {
        type: 'object',
        properties: {
          username: { type: 'string', description: 'Username who owns the collection' },
          collection_name: { type: 'string', description: 'Name of the collection' },
          message: { type: 'string', description: 'Commit message describing the changes' },
        },
        required: ['username', 'collection_name'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'fork_collection',
      description: 'Fork a collection from another user into your own namespace.',
      parameters: {
        type: 'object',
        properties: {
          username: { type: 'string', description: 'Your username (target)' },
          source_username: { type: 'string', description: 'Source username to fork from' },
          collection_name: { type: 'string', description: 'Name for the new forked collection' },
          source_collection: { type: 'string', description: 'Name of the source collection' },
        },
        required: ['username', 'source_username', 'collection_name', 'source_collection'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'get_version_log',
      description: 'View the commit history of a collection.',
      parameters: {
        type: 'object',
        properties: {
          username: { type: 'string', description: 'Username who owns the collection' },
          collection_name: { type: 'string', description: 'Name of the collection' },
        },
        required: ['username', 'collection_name'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'register_local_file',
      description: 'Register a local file path for tracking in PeerDrive.',
      parameters: {
        type: 'object',
        properties: {
          path: { type: 'string', description: 'Absolute or relative file path to register' },
          filename: { type: 'string', description: 'Custom filename (optional, derived from path if omitted)' },
        },
        required: ['path'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'register_folder',
      description: 'Register a local folder to track all files within it.',
      parameters: {
        type: 'object',
        properties: {
          folder_path: { type: 'string', description: 'Path to the folder to register' },
        },
        required: ['folder_path'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'create_anon_collection',
      description: 'Create a new anonymous collection. entries are { path, hash } objects.',
      parameters: {
        type: 'object',
        properties: {
          entries: { type: 'array', items: { type: 'object', properties: { path: { type: 'string' }, hash: { type: 'string' } } }, description: 'Array of { path, hash } entries' },
          friendly_name: { type: 'string', description: 'Optional display name for the collection' },
        },
        required: ['entries'],
      },
    },
  },
  {
    type: 'function',
    function: {
      name: 'verify_file',
      description: 'Verify a file exists on the node by its SHA256 hash.',
      parameters: {
        type: 'object',
        properties: {
          hash: { type: 'string', description: 'SHA256 hash of the file (64 hex chars)' },
        },
        required: ['hash'],
      },
    },
  },
];

async function executeTool(name, args, navigate) {
  try {
    switch (name) {
      case 'navigate_to': {
        const route = args.route || '/';
        navigate(route);
        return `Navigated to ${route}`;
      }
      case 'get_node_info': {
        await api.ping();
        // 2026-08-19：旧 getNodeInfo（/p2p/node libp2p 端点）已删，改查 /peerjs/node
        const info = await api.getPeerjsNode();
        return JSON.stringify(info);
      }
      case 'list_collections': {
        if (args.username) {
          const colls = await api.getUserCollections(args.username);
          return JSON.stringify(colls);
        }
        const anonColls = await api.listAnonCollections();
        return JSON.stringify(anonColls);
      }
      case 'list_public_collections': {
        const list = await api.listPublicCollections(args.q || '');
        return JSON.stringify(list);
      }
      case 'search_collections': {
        const results = await api.searchCollections(args.q);
        return JSON.stringify(results);
      }
      case 'create_collection': {
        const result = await api.createUserCollection(args.username, args.collection_name, args.visibility || 'public');
        return JSON.stringify(result);
      }
      case 'get_collection_info': {
        const info = await api.getUserCollection(args.username, args.collection_name);
        return JSON.stringify(info);
      }
      case 'add_file_to_collection': {
        const result = await api.addCollectionEntry(args.username, args.collection_name, args.path, args.hash);
        return JSON.stringify(result);
      }
      case 'commit_collection': {
        const result = await api.commitCollection(args.username, args.collection_name, args.message || '');
        return JSON.stringify(result);
      }
      case 'fork_collection': {
        const result = await api.forkUserCollection(args.username, args.source_username, args.collection_name, args.source_collection);
        return JSON.stringify(result);
      }
      case 'get_version_log': {
        const log = await api.getVersionLog(args.username, args.collection_name);
        return JSON.stringify(log);
      }
      case 'register_local_file': {
        const result = await api.registerLocalFile(args.path, args.filename || '');
        return JSON.stringify(result);
      }
      case 'register_folder': {
        const result = await api.registerFolder(args.folder_path);
        return JSON.stringify(result);
      }
      case 'create_anon_collection': {
        const result = await api.createAnonCollection(args.entries || [], args.friendly_name || '');
        return JSON.stringify(result);
      }
      case 'verify_file': {
        const result = await api.verifyFile(args.hash);
        return JSON.stringify(result);
      }
      default:
        return `Unknown tool: ${name}`;
    }
  } catch (e) {
    return `Error: ${e.message}`;
  }
}

export default function LLMAssistant() {
  const [open, setOpen] = useState(false);
  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [streamText, setStreamText] = useState('');
  const [toolStatus, setToolStatus] = useState('');
  const chatRef = useRef(null);
  const abortRef = useRef(null);
  const location = useLocation();
  const navigate = useNavigate();
  const { pageContext } = useContext(PageContext) || {};

  useEffect(() => {
    if (chatRef.current) {
      chatRef.current.scrollTop = chatRef.current.scrollHeight;
    }
  }, [messages, streamText, toolStatus]);

  // 每次页面切换时重建上下文，确保 LLM 知道当前在哪
  const [currentContext, setCurrentContext] = useState({});
  useEffect(() => {
    setCurrentContext({ route: location.pathname, timestamp: new Date().toISOString(), pageData: pageContext || null });
  }, [location.pathname, pageContext]);

  const buildContext = () => currentContext;

  const handleStopStream = () => {
    if (abortRef.current) {
      abortRef.current.abort();
      abortRef.current = null;
    }
    setStreaming(false);
    setToolStatus('');
  };

  const streamLLM = async (body, apiKey, signal) => {
    const endpoint = api.getLlmEndpoint();
    const headers = { 'Content-Type': 'application/json' };
    if (apiKey) headers['Authorization'] = `Bearer ${apiKey}`;

    const res = await fetch(`${endpoint}/v1/chat/completions`, {
      method: 'POST',
      headers,
      body: JSON.stringify(body),
      signal,
    });

    if (!res.ok) {
      const errText = await res.text().catch(() => `HTTP ${res.status}`);
      throw new Error(errText);
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let fullText = '';
    let buffer = '';
    const toolCalls = {};

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
          const delta = json.choices?.[0]?.delta;
          if (!delta) continue;

          // 显示 thinking 过程（DeepSeek/Qwen reasoning）
          if (delta.reasoning_content || json.choices?.[0]?.delta?.reasoning_content) {
            fullText += '💭' + (delta.reasoning_content || json.choices[0].delta.reasoning_content) + '\n';
            setStreamText(fullText);
          }
          if (delta.content) {
            fullText += delta.content;
            setStreamText(fullText);
          }

          for (const tc of delta.tool_calls || []) {
            const idx = tc.index;
            if (!toolCalls[idx]) toolCalls[idx] = { id: '', name: '', arguments: '' };
            if (tc.id) toolCalls[idx].id = tc.id;
            if (tc.function?.name) toolCalls[idx].name = tc.function.name;
            if (tc.function?.arguments) toolCalls[idx].arguments += tc.function.arguments;
          }

          if (json.choices?.[0]?.finish_reason && fullText) {
            setStreamText(fullText);
          }
        } catch {}
      }
    }

    const tcArray = Object.values(toolCalls).filter(tc => tc.name);
    return { text: fullText, toolCalls: tcArray };
  };

  const handleSend = async () => {
    if (!input.trim() || streaming) return;
    const userMsg = { role: 'user', content: input };
    setInput('');
    setStreaming(true);
    setStreamText('');
    setToolStatus('');

    const model = api.getLlmModel();
    const apiKey = api.getLlmApiKey();

    let baseBody;
    try {
      baseBody = JSON.parse(api.getLlmBodyTemplate());
    } catch {
      baseBody = { model, messages: [], stream: true };
    }
    baseBody.model = model || baseBody.model;

    const systemContent = SYSTEM_PROMPT + '\n\nCurrent page context:\n```json\n' + JSON.stringify(buildContext(), null, 2) + '\n```';

    // 坑：旧实现每轮 setMessages([userMsg]) / displayMessages=[userMsg] ——
    // 上一轮对话被清空，助手无跨轮记忆。改为携带最近 20 条历史（角色仅 user/assistant，
    // 不含 system，可安全回放），同时在 UI 中追加而非替换。
    const historyMessages = messages.slice(-20);
    const accMessages = [
      { role: 'system', content: systemContent },
      ...historyMessages,
      userMsg,
    ];
    let displayMessages = [...historyMessages, userMsg];
    setMessages(displayMessages);

    let turns = 0;
    const MAX_TURNS = 5;

    try {
      while (turns < MAX_TURNS) {
        turns++;
        const controller = new AbortController();
        abortRef.current = controller;

        baseBody.messages = [...accMessages];
        baseBody.tools = TOOLS;
        const result = await streamLLM(baseBody, apiKey, controller.signal);

        if (result.toolCalls.length > 0) {
          for (const tc of result.toolCalls) {
            const callId = tc.id || `call_${Date.now()}`;
            const toolCallMsg = {
              role: 'assistant',
              content: null,
              tool_calls: [{ id: callId, type: 'function', function: { name: tc.name, arguments: tc.arguments } }],
            };
            accMessages.push(toolCallMsg);

            let args = {};
            try { args = JSON.parse(tc.arguments); } catch {}
            setToolStatus(`🛠️ ${tc.name}(${Object.values(args).slice(0, 3).join(', ')})...`);
            const toolResult = await executeTool(tc.name, args, navigate);
            const toolMsg = {
              role: 'tool',
              tool_call_id: callId,
              content: typeof toolResult === 'string' ? toolResult : JSON.stringify(toolResult),
            };
            accMessages.push(toolMsg);
            setToolStatus('');
          }
          setStreamText('');
          continue;
        }

        const finalText = result.text || '(empty response)';
        const assistantMsg = { role: 'assistant', content: finalText };
        accMessages.push(assistantMsg);
        displayMessages.push(assistantMsg);
        setMessages([...displayMessages]);
        setStreamText('');
        break;
      }

      if (turns >= MAX_TURNS) {
        const stopMsg = { role: 'assistant', content: '(stopped after max steps)' };
        displayMessages.push(stopMsg);
        setMessages([...displayMessages]);
      }
    } catch (e) {
      if (e.name !== 'AbortError') {
        const errMsg = { role: 'assistant', content: `❌ ${e.message}` };
        displayMessages.push(errMsg);
        setMessages([...displayMessages]);
      }
      setStreamText('');
      setToolStatus('');
    }
    setStreaming(false);
    abortRef.current = null;
  };

  const [skin, setSkin] = useState(() => localStorage.getItem('peerdrive_skin') || 'clippy');

  const skins = {
    clippy: { icon: '📎', label: 'Clippy', btn: 'bg-amber-500 hover:bg-amber-400' },
    minimal: { icon: '💬', label: 'Minimal', btn: 'bg-blue-600 hover:bg-blue-700' },
  };
  const activeSkin = skins[skin] || skins.clippy;

  return (
    <>
      {!open && (
        <div className="fixed bottom-6 right-6 z-40 flex flex-col items-end gap-2">
          <button
            onClick={() => setOpen(true)}
            className={`w-14 h-14 ${activeSkin.btn} rounded-full shadow-lg flex items-center justify-center text-2xl transition-transform hover:scale-110 animate-bounce shrink-0`}
            title={`AI 助手 (${activeSkin.label})`}
          >
            {activeSkin.icon}
          </button>
          <button
            onClick={() => { const s = skin === 'clippy' ? 'minimal' : 'clippy'; setSkin(s); localStorage.setItem('peerdrive_skin', s); }}
            className="text-[10px] text-gray-600 hover:text-gray-400 bg-gray-900/80 px-2 py-0.5 rounded-full"
          >
            {skin === 'clippy' ? '💬' : '📎'}
          </button>
        </div>
      )}
      {open && (
        <div className="fixed bottom-6 right-3 md:right-6 w-[calc(100vw-1.5rem)] md:w-96 h-[560px] max-h-[75vh] bg-gray-900 border border-gray-700 rounded-xl shadow-2xl flex flex-col z-40">
          <div className="flex items-center justify-between px-4 py-3 border-b border-gray-700 shrink-0">
            <div className="flex items-center gap-2">
              <span className="text-lg">{activeSkin.icon}</span>
              <span className="font-bold text-sm">{activeSkin.label}</span>
              <span className="text-[10px] text-gray-500">{api.getLlmModel()}</span>
            </div>
            <button onClick={() => setOpen(false)} className="text-gray-400 hover:text-white text-xl leading-none">&times;</button>
          </div>

          <div ref={chatRef} className="flex-1 overflow-y-auto p-4 space-y-3">
            {messages.length === 0 && (
              <div className="text-center pt-10 px-4">
                <p className="text-4xl mb-3">{activeSkin.icon}</p>
                <p className="text-gray-300 text-sm font-medium">看起来你想管理文件？</p>
                <p className="text-gray-500 text-xs mt-2">我可以帮你：创建合集、注册文件、搜索 P2P 网络、管理 BT 下载。</p>
                <p className="text-gray-600 text-[10px] mt-3">直接告诉我你想做什么就行。</p>
              </div>
            )}
            {messages.map((m, i) => {
              if (m.role === 'tool' || (m.role === 'assistant' && m.tool_calls)) return null;
              return (
                <div key={i} className={`flex ${m.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[85%] rounded-lg px-3 py-2 text-sm ${
                    m.role === 'user'
                      ? 'bg-blue-600 text-white'
                      : 'bg-gray-800 text-gray-200 border border-gray-700'
                  }`}>
                    <pre className="whitespace-pre-wrap font-sans break-words">{m.content}</pre>
                  </div>
                </div>
              );
            })}
            {toolStatus && (
              <div className="flex justify-start">
                <div className="max-w-[85%] rounded-lg px-3 py-2 text-sm bg-gray-800 text-yellow-400 border border-yellow-700/50">
                  {toolStatus}
                </div>
              </div>
            )}
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
