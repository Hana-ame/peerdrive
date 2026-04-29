import FileTree from '../../components/FileTree';
import EditorToolbar from './EditorToolbar';
import NamePrompt from './NamePrompt';

export default function EditorPanel({ fname, tags, entries, saving, showNamePrompt, toastMsg, toastErr, entryActions, onFname, onTags, onSave, onAiName, onCloseNamePrompt }) {
  const validCount = entries.filter(e => e.path?.trim() && (e.path.endsWith('/') || e.hash || e.providers?.[0]?.value)).length;

  return (
    <div className="w-1/2 flex flex-col min-h-[200px]">
      {toastMsg && (
        <div className={`px-3 py-1 text-xs shrink-0 ${toastErr ? 'text-red-400 bg-red-500/10' : 'text-green-400 bg-green-500/10'}`}>{toastMsg}</div>
      )}

      <EditorToolbar fname={fname} tags={tags} entryCount={entries.length}
        validCount={validCount} saving={saving}
        onFname={onFname} onTags={onTags} onAiName={onAiName} onSave={() => onSave(false)} />

      {showNamePrompt && (
        <NamePrompt onAiName={() => onSave(true)} onSkip={() => onSave(false)} onClose={onCloseNamePrompt} />
      )}

      <div className="flex-1 overflow-hidden">
        <FileTree entries={entries} entryActions={entryActions} />
      </div>
    </div>
  );
}
