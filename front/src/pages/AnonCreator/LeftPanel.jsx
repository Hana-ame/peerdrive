import { SOURCE_TABS, LEFT_SORT_OPTS, LEFT_TYPE_FILTERS, COLL_SORT_OPTS } from './constants';
import CollectionRow from './CollectionRow';
import CollBrowser from './CollBrowser';
import FileSourceRow from './FileSourceRow';
import SystemBrowse from './SystemBrowse';
import SearchHistory from './SearchHistory';
import { addSearchHistory, loadSearchHistory } from './utils';
import { useState } from 'react';

export default function LeftPanel({
  // 来源 & 筛选
  sourceTab, sortKey, sortOrder, typeFilters,
  // 文件数据
  files, filteredFiles, search,
  // 合集数据
  collections, filteredCollections,
  collSearch, collTagFilter, collSort,
  allCollTags,
  // 合集浏览
  enteredColl, collViewPath, enteredCollFiles,
  // 选择模式
  selectMode, selectedColls, selectedFiles,
  // 系统浏览
  sysPath, sysEntries, sysLoading,
  // 搜索历史
  searchHistory, showHistory,
  // 回调 - 筛选
  onSourceTab, onSortKey, onSortOrder, onTypeFilter, onSearch,
  // 回调 - 文件
  onFileSelect, onFileAdd, onDragStart,
  // 回调 - 合集
  onCollSearch, onCollTag, onCollSort,
  onEnterColl, onLeaveColl, onPathNav, onNavIntoDir,
  onSaveToNode, onSelectToggle, onToggleCollSelect,
  onToggleFileSelect, onBatchSaveColls, onBatchSaveFiles,
  // 回调 - 系统浏览
  onSysNav, onSysAdd,
  // 回调 - 搜索历史
  onSearchHistorySelect, onSearchHistoryShow, onSearchHistoryUpdate,
}) {
  const [localShowHistory, setLocalShowHistory] = useState(false);

  const toggleType = (typeId) => {
    if (typeFilters.includes(typeId)) {
      onTypeFilter(typeFilters.filter(t => t !== typeId));
    } else {
      onTypeFilter([...typeFilters, typeId]);
    }
  };

  const handleCollSearchChange = (val) => {
    onCollSearch(val);
    setLocalShowHistory(true);
  };

  const handleCollSearchSelect = (q) => {
    onCollSearch(q);
    setLocalShowHistory(false);
    addSearchHistory(q);
    if (onSearchHistoryUpdate) onSearchHistoryUpdate(loadSearchHistory());
  };

  const handleCollSearchKeyDown = (e) => {
    if (e.key === 'Enter') {
      addSearchHistory(collSearch);
      if (onSearchHistoryUpdate) onSearchHistoryUpdate(loadSearchHistory());
      setLocalShowHistory(false);
    }
  };

  const isCollectionsTab = sourceTab === 'collections';

  return (
    <div className="h-full flex flex-col bg-gray-900 border-r border-gray-800">
      {/* 第一排：来源选择 */}
      <div className="flex gap-1 p-2 border-b border-gray-800 shrink-0">
        {SOURCE_TABS.map(tab => (
          <button
            key={tab.id}
            onClick={() => onSourceTab(tab.id)}
            className={`flex-1 text-xs px-2 py-1.5 rounded transition-colors ${
              sourceTab === tab.id
                ? 'bg-blue-600 text-white font-medium'
                : 'bg-gray-800 text-gray-400 hover:text-white hover:bg-gray-700'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {/* 合集模式：显示合集搜索/排序/标签筛选 */}
      {isCollectionsTab && (
        <div className="shrink-0 bg-gray-900/50 border-b border-gray-800">
          <div className="flex items-center gap-2 px-3 pt-2 pb-1">
            <h3 className="text-sm font-bold text-gray-300 flex items-center gap-1">📦 合集</h3>
            <span className="text-[10px] text-gray-600">{collections.length}</span>
            <div className="flex-1" />
            <button onClick={onSelectToggle} className={`text-[10px] px-2 py-0.5 rounded ${selectMode ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'}`}>
              {selectMode ? '退出选择' : '选择'}
            </button>
          </div>

          <div className="px-2 pb-1 relative">
            <input value={collSearch} onChange={e => handleCollSearchChange(e.target.value)}
              onFocus={() => setLocalShowHistory(true)}
              onBlur={() => setTimeout(() => setLocalShowHistory(false), 200)}
              onKeyDown={handleCollSearchKeyDown}
              placeholder="搜索合集名称 / tag..." className="w-full bg-gray-800 text-xs px-3 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
            <SearchHistory visible={localShowHistory && searchHistory.length > 0 && !collSearch} searchHistory={searchHistory}
              onSelect={handleCollSearchSelect} onClear={() => {
                localStorage.removeItem('peerdrive_search_history');
                if (onSearchHistoryUpdate) onSearchHistoryUpdate([]);
              }} />
          </div>

          <div className="px-2 pb-1.5 space-y-1">
            <div className="flex gap-0.5">
              {COLL_SORT_OPTS.map(o => (
                <button key={o.v} onClick={() => onCollSort(o.v)}
                  className={`text-[10px] px-2 py-0.5 rounded ${collSort===o.v?'bg-blue-600 text-white':'bg-gray-800 text-gray-400 hover:text-white'}`}>{o.l}</button>
              ))}
            </div>
            {allCollTags.length > 0 && (
              <div className="flex gap-0.5 flex-wrap">
                <button onClick={() => onCollTag('')}
                  className={`text-[10px] px-1.5 py-0.5 rounded-full ${!collTagFilter ? 'bg-blue-600 text-white' : 'bg-gray-800 text-gray-400'}`}>全部</button>
                {allCollTags.map(t => (
                  <button key={t} onClick={() => onCollTag(t === collTagFilter ? '' : t)}
                    className={`text-[10px] px-1.5 py-0.5 rounded-full ${collTagFilter===t?'bg-blue-600 text-white':'bg-gray-700 text-gray-300 hover:bg-gray-600'}`}>{t}</button>
                ))}
              </div>
            )}
          </div>

          {selectMode && selectedColls.size > 0 && (
            <div className="px-2 pb-1.5">
              <button onClick={onBatchSaveColls}
                className="w-full text-xs bg-blue-600 hover:bg-blue-700 px-2 py-1 rounded">
                保存选中的 {selectedColls.size} 个合集到本机
              </button>
            </div>
          )}
        </div>
      )}

      {/* 非合集、非本地模式：排序 + 类型筛选 */}
      {!isCollectionsTab && sourceTab !== 'local' && (
        <div className="shrink-0 bg-gray-900/50 border-b border-gray-800">
          {/* 第二排：排序选项 */}
          <div className="flex items-center justify-between p-2 pb-1">
            <div className="flex gap-1 flex-1 mr-2">
              {LEFT_SORT_OPTS.map(opt => (
                <button
                  key={opt.v}
                  onClick={() => onSortKey(opt.v)}
                  className={`text-[10px] px-1.5 py-1 rounded flex-1 truncate ${
                    sortKey === opt.v
                      ? 'bg-blue-600/30 text-blue-300'
                      : 'text-gray-500 hover:text-gray-300'
                  }`}
                  title={opt.l}
                >
                  {opt.l}
                </button>
              ))}
            </div>
            <button
              onClick={onSortOrder}
              className="text-gray-400 hover:text-white text-sm px-2 py-1 rounded hover:bg-gray-800 transition-colors shrink-0"
              title={sortOrder === 'asc' ? '升序' : '降序'}
            >
              {sortOrder === 'asc' ? '↑ 升序' : '↓ 降序'}
            </button>
          </div>

          {/* 第三排：类型筛选（多选） */}
          <div className="flex gap-1 p-2 pt-0">
            {LEFT_TYPE_FILTERS.map(type => {
              const active = typeFilters.includes(type.id);
              return (
                <button
                  key={type.id}
                  onClick={() => toggleType(type.id)}
                  className={`flex items-center gap-1 text-[10px] px-2 py-1 rounded border transition-all flex-1 justify-center ${
                    active
                      ? 'bg-blue-600/20 border-blue-500 text-blue-300'
                      : 'bg-gray-800 border-gray-700 text-gray-500 hover:border-gray-600 hover:text-gray-300'
                  }`}
                >
                  <span>{type.icon}</span>
                  <span>{type.label}</span>
                </button>
              );
            })}
          </div>

          {/* 搜索框 */}
          <div className="px-2 pb-2">
            <input value={search} onChange={e => onSearch(e.target.value)}
              placeholder="搜索文件名..." className="w-full bg-gray-800 text-xs px-3 py-1.5 rounded border border-gray-700 focus:outline-none focus:border-blue-600" />
            <div className="text-[10px] text-gray-600 mt-1 px-1">{filteredFiles.length} 个文件</div>
          </div>
        </div>
      )}

      {/* 文件/合集列表区域 */}
      <div className="flex-1 overflow-y-auto">
        {/* 合集模式 */}
        {isCollectionsTab && (
          enteredColl ? (
            <CollBrowser coll={enteredColl} collViewPath={collViewPath}
              selectMode={selectMode} selectedFiles={selectedFiles}
              onLeave={onLeaveColl} onPathNav={onPathNav} onNavIntoDir={onNavIntoDir}
              onSaveToNode={onSaveToNode} onSelectToggle={onSelectToggle}
              onToggleFileSelect={onToggleFileSelect} onBatchSaveFiles={onBatchSaveFiles}
              onFileAdd={onFileAdd} />
          ) : (
            <>
              {filteredCollections.length === 0 ? (
                <p className="p-4 text-gray-600 text-xs text-center">
                  {collSearch || collTagFilter ? '无匹配合集' : '暂无合集，创建第一个吧 →'}
                </p>
              ) : (
                filteredCollections.map(c => (
                  <CollectionRow key={c.hash} collection={c} selectMode={selectMode}
                    isSelected={selectedColls.has(c.hash)}
                    onClick={onEnterColl} onToggleSelect={onToggleCollSelect} />
                ))
              )}
            </>
          )
        )}

        {/* 本地电脑模式 */}
        {sourceTab === 'local' && (
          <SystemBrowse sysPath={sysPath} sysEntries={sysEntries} sysLoading={sysLoading}
            onNavTo={onSysNav} onAdd={onSysAdd} onDragStart={onDragStart} />
        )}

        {/* 所有文件：按时间分组 */}
        {sourceTab === 'all' && (
          filteredFiles.length === 0 ? (
            <p className="p-4 text-gray-600 text-xs text-center">无匹配文件</p>
          ) : (
            (() => {
              const byDate = [...filteredFiles].sort((a, b) => (b.created_at || '').localeCompare(a.created_at || ''));
              let lastDate = '';
              return byDate.map(f => {
                const d = f.created_at ? f.created_at.split('T')[0] : '';
                const showDate = d !== lastDate;
                lastDate = d;
                return (
                  <div key={f.hash || f.filename}>
                    {showDate && <div className="px-4 py-2 text-[10px] text-gray-500 bg-gray-900/50 sticky top-0">{d || '未知日期'}</div>}
                    <FileSourceRow file={f} onAdd={onFileAdd} onDragStart={onDragStart}
                      onSelect={() => onFileSelect({ hash: f.hash, path: f.filename, filename: f.filename, mime_type: f.mime_type, size: f.size })} />
                  </div>
                );
              });
            })()
          )
        )}

        {/* 已注册：按目录分组（同旧 RegisteredView） */}
        {sourceTab === 'registered' && (
          filteredFiles.length === 0 ? (
            <p className="p-4 text-gray-600 text-xs text-center">无匹配文件</p>
          ) : (
            (() => {
              const dirs = new Set();
              const rootFiles = [];
              for (const f of filteredFiles) {
                const rel = (f.provider_path || f.filename || '').replace(/^\//, '');
                const slash = rel.indexOf('/');
                if (slash === -1) rootFiles.push(f);
                else if (rel.slice(0, slash)) dirs.add(rel.slice(0, slash));
              }
              return (
                <>
                  {Array.from(dirs).sort().map(dir => (
                    <div key={dir} className="flex items-center gap-3 px-4 py-2.5 hover:bg-gray-800 cursor-pointer border-b border-gray-800/50 text-sm">
                      <span className="text-lg">📁</span>
                      <span className="text-yellow-400 font-mono truncate flex-1 text-xs">{dir}</span>
                      <span className="text-gray-600 text-xs">文件夹</span>
                    </div>
                  ))}
                  {rootFiles.map(f => (
                    <FileSourceRow key={f.hash || f.filename} file={f} onAdd={onFileAdd} onDragStart={onDragStart}
                      onSelect={() => onFileSelect({ hash: f.hash, path: f.filename, filename: f.filename, mime_type: f.mime_type, size: f.size })} />
                  ))}
                </>
              );
            })()
          )
        )}
      </div>
    </div>
  );
}
