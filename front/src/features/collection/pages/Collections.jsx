import React from 'react';
import { useNavigate } from 'react-router-dom';
import DataState from '../../../components/netdisk/DataState';
import CreateCollectionForm from '../components/CreateCollectionForm';
import CollectionHeader from '../components/CollectionHeader';
import CollectionToolbar from '../components/CollectionToolbar';
import CollectionGridView from '../components/CollectionGridView';
import CollectionTableView from '../components/CollectionTableView';
import { useCollections } from '../hooks/useCollections';
import { VIS_LABEL, fmtTime } from '../utils/collectionUtils';

export { VIS_LABEL, fmtTime };

export default function Collections() {
  const navigate = useNavigate();
  const state = useCollections();

  const emptyProps = {
    icon: '📦',
    title: state.isGuest
      ? state.collections?.length === 0
        ? 'No shared collections'
        : 'No matching collections found'
      : state.collections?.length === 0
      ? 'No collections yet'
      : 'No matching collections found',
    description: state.isGuest
      ? state.collections?.length === 0
        ? 'This remote node has not shared any public collections.'
        : 'Try clearing your search query or tag filter.'
      : state.collections?.length === 0
      ? 'Click "New Collection" to create one from your netdisk files.'
      : 'Try clearing your search query or tag filter.',
    actionLabel: !state.isGuest && state.canCreate && state.collections?.length === 0 ? '+ New Collection' : null,
    onAction: !state.isGuest && state.canCreate ? state.openCreate : null,
  };

  return (
    <div className="p-4 sm:p-8 overflow-y-auto overflow-x-hidden h-full max-w-full">
      <div className="max-w-5xl mx-auto">
        <CollectionHeader
          navigate={navigate}
          isGuest={state.isGuest}
          tab={state.tab}
          setTab={state.setTab}
          viewMode={state.viewMode}
          setViewMode={state.setViewMode}
          loadList={state.loadList}
          canCreate={state.canCreate}
          openCreate={state.openCreate}
        />

        {state.err && (
          <div className="mb-4 text-xs text-red-400 bg-red-400/10 border border-red-400/20 rounded-lg px-3 py-2">
            {state.err}
          </div>
        )}

        {state.tab === 'list' && (
          <div>
            <CollectionToolbar
              searchQuery={state.searchQuery}
              setSearchQuery={state.setSearchQuery}
              allTagsWithCounts={state.allTagsWithCounts}
              selectedTag={state.selectedTag}
              setSelectedTag={state.setSelectedTag}
              totalCollections={state.collections?.length || 0}
            />

            <DataState
              loading={state.collections === null}
              error={state.err}
              empty={state.filteredCollections.length === 0}
              onRetry={state.loadList}
              skeletonType={state.viewMode === 'grid' ? 'cards' : 'table'}
              skeletonRows={6}
              emptyProps={emptyProps}
            >
              {state.viewMode === 'grid' ? (
                <CollectionGridView
                  collections={state.filteredCollections}
                  openDetail={state.openDetail}
                />
              ) : (
                <CollectionTableView
                  collections={state.filteredCollections}
                  openDetail={state.openDetail}
                  detail={state.detail}
                  navigate={navigate}
                  unlockPasscode={state.unlockPasscode}
                  setUnlockPasscode={state.setUnlockPasscode}
                  unlockProtected={state.unlockProtected}
                  downloadEntry={state.downloadEntry}
                  entryHash={state.entryHash}
                  busy={state.busy}
                />
              )}
            </DataState>
          </div>
        )}

        {state.tab === 'create' && state.canCreate && (
          <CreateCollectionForm
            name={state.name}
            setName={state.setName}
            tagsInput={state.tagsInput}
            setTagsInput={state.setTagsInput}
            visibility={state.visibility}
            setVisibility={state.setVisibility}
            accessPolicy={state.accessPolicy}
            setAccessPolicy={state.setAccessPolicy}
            passcode={state.passcode}
            setPasscode={state.setPasscode}
            files={state.files}
            selected={state.selected}
            toggleFile={state.toggleFile}
            create={state.create}
            busy={state.busy}
            onCancel={() => state.setTab('list')}
            visLabels={VIS_LABEL}
          />
        )}
      </div>
    </div>
  );
}
