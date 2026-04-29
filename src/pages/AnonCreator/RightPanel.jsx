import SourceTabs from './SourceTabs';
import SourceFilters from './SourceFilters';
import TimelineView from './TimelineView';
import RegisteredView from './RegisteredView';
import SystemBrowse from './SystemBrowse';
import EditorPanel from './EditorPanel';

export default function RightPanel({ width, srcTab, sort, typeF, search, filtered, sysPath, sysEntries, sysLoading, onSrcTab, onSort, onType, onSearch, onAdd, onDragStart, onSysNav, onSysAdd,
  // Editor props
  fname, tags, entries, saving, showNamePrompt, toastMsg, toastErr, entryActions, onFname, onTags, onSave, onAiName, onCloseNamePrompt }) {

  return (
    <div style={{ width: `${100 - width}%` }} className="h-full flex flex-row">
      <div className="w-1/2 flex flex-col overflow-hidden border-r border-gray-700">
        <SourceTabs active={srcTab} onChange={onSrcTab} onSystemReset={() => onSysNav('/')} />

        {srcTab === 'timeline' && (
          <SourceFilters sort={sort} typeF={typeF} search={search}
            filteredCount={filtered.length} onSort={onSort} onType={onType} onSearch={onSearch} />
        )}

        {srcTab === 'registered' && (
          <>
            <input value={search} onChange={e => onSearch(e.target.value)} placeholder="搜索文件路径..."
              className="bg-gray-800 text-xs px-3 py-1.5 border-b border-gray-800 focus:outline-none focus:border-blue-600" />
            <span className="text-[10px] text-gray-600 px-3 py-1">{filtered.length} 个文件</span>
          </>
        )}

        <div className="flex-1 overflow-y-auto">
          {srcTab === 'timeline' && <TimelineView filtered={filtered} onAdd={onAdd} onDragStart={onDragStart} />}
          {srcTab === 'registered' && <RegisteredView filtered={filtered} onAdd={onAdd} onDragStart={onDragStart} />}
          {srcTab === 'system' && (
            <SystemBrowse sysPath={sysPath} sysEntries={sysEntries} sysLoading={sysLoading}
              onNavTo={onSysNav} onAdd={onSysAdd} onDragStart={onDragStart} />
          )}
        </div>
      </div>

      <EditorPanel fname={fname} tags={tags} entries={entries} saving={saving}
        showNamePrompt={showNamePrompt} toastMsg={toastMsg} toastErr={toastErr}
        entryActions={entryActions}
        onFname={onFname} onTags={onTags} onSave={onSave} onAiName={onAiName}
        onCloseNamePrompt={onCloseNamePrompt} />
    </div>
  );
}
