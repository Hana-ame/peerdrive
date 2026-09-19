import EditorPanel from './EditorPanel';

export default function RightPanel({
  fname, tags, entries, saving, showNamePrompt, toastMsg, toastErr,
  entryActions, onFname, onTags, onSave, onAddUrl, onCloseNamePrompt,
  visibility, accessList, operator, onVisibilityChange, onRequestAccounts, onBroadcast,
}) {
  return (
    <div className="h-full flex flex-col">
      <EditorPanel fname={fname} tags={tags} entries={entries} saving={saving}
        showNamePrompt={showNamePrompt} toastMsg={toastMsg} toastErr={toastErr}
        entryActions={entryActions}
        visibility={visibility} accessList={accessList} operator={operator}
        onFname={onFname} onTags={onTags} onSave={onSave}
        onAddUrl={onAddUrl} onCloseNamePrompt={onCloseNamePrompt}
        onVisibilityChange={onVisibilityChange} onRequestAccounts={onRequestAccounts}
        onBroadcast={onBroadcast} />
    </div>
  );
}
