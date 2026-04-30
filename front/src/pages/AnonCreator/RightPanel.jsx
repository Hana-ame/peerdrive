import EditorPanel from './EditorPanel';

export default function RightPanel({
  fname, tags, entries, saving, showNamePrompt, toastMsg, toastErr,
  entryActions, onFname, onTags, onSave, onAiName, onCloseNamePrompt,
}) {
  return (
    <div className="h-full flex flex-col">
      <EditorPanel fname={fname} tags={tags} entries={entries} saving={saving}
        showNamePrompt={showNamePrompt} toastMsg={toastMsg} toastErr={toastErr}
        entryActions={entryActions}
        onFname={onFname} onTags={onTags} onSave={onSave}
        onAiName={onAiName} onCloseNamePrompt={onCloseNamePrompt} />
    </div>
  );
}
