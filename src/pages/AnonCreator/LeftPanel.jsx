import CollectionHeader from './CollectionHeader';
import CollectionRow from './CollectionRow';
import CollBrowser from './CollBrowser';

export default function LeftPanel({ width, collections, filteredCollections, collSearch, collTagFilter, collSort, selectMode, selectedColls, allCollTags, searchHistory, showHistory, enteredColl, collViewPath, enteredCollFiles, selectedFiles, onSearch, onTag, onSort, onSelectToggle, onToggleCollSelect, onBatchSaveColls, onSearchHistorySelect, onSearchHistoryShow, onEnterColl, onLeaveColl, onPathNav, onNavIntoDir, onSaveToNode, onToggleFileSelect, onBatchSaveFiles, onFileAdd }) {
  return (
    <div style={{ width: `${width}%` }} className="h-full flex flex-col border-r border-gray-700">
      <CollectionHeader collSearch={collSearch} collTagFilter={collTagFilter} collSort={collSort}
        selectMode={selectMode} selectedCount={selectedColls.size}
        allCollTags={allCollTags} searchHistory={searchHistory}
        showHistory={showHistory} collectionsLen={collections.length}
        onSearch={onSearch} onTag={onTag} onSort={onSort}
        onSelectToggle={onSelectToggle} onBatchSave={onBatchSaveColls}
        onSearchHistorySelect={onSearchHistorySelect} />

      <div className="flex-1 overflow-y-auto">
        {enteredColl ? (
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
        )}
      </div>
    </div>
  );
}
