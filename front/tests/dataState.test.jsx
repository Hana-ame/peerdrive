// dataState.test.jsx — unit tests for DataState component and errorClassifier.
//
// Discovery context (issue #149): ensures skeleton loading, empty state with CTAs,
// and classified error states with retry are rendered without visual disruption.

import { describe, it, expect, vi } from 'vitest';
import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import DataState from '../src/components/netdisk/DataState';
import { classifyError, ERROR_CATEGORIES } from '../src/platform/shared/errorClassifier';

describe('errorClassifier', () => {
  it('correctly categorizes disconnected errors', () => {
    const res = classifyError(new Error('WebSocket connection closed'));
    expect(res.category).toBe(ERROR_CATEGORIES.DISCONNECTED);
    expect(res.title).toBe('Node Disconnected');
  });

  it('correctly categorizes unauthorized / PSK errors', () => {
    const res = classifyError(new Error('PSK_REQUIRED'));
    expect(res.category).toBe(ERROR_CATEGORIES.UNAUTHORIZED);
    expect(res.title).toBe('Access Restricted');
  });

  it('correctly categorizes timeout errors', () => {
    const res = classifyError(new Error('Request timed out after 10000ms'));
    expect(res.category).toBe(ERROR_CATEGORIES.TIMEOUT);
    expect(res.title).toBe('Request Timed Out');
  });

  it('correctly categorizes unreachable peer errors', () => {
    const res = classifyError(new Error('peer unreachable'));
    expect(res.category).toBe(ERROR_CATEGORIES.UNREACHABLE);
    expect(res.title).toBe('Peer Unreachable');
  });

  it('correctly categorizes missing resource errors', () => {
    const res = classifyError(new Error('manifest not found'));
    expect(res.category).toBe(ERROR_CATEGORIES.MISSING);
    expect(res.title).toBe('Resource Not Found');
  });
});

describe('DataState component', () => {
  it('renders skeleton on loading', () => {
    render(
      <DataState loading={true}>
        <div>Actual Content</div>
      </DataState>
    );
    expect(screen.getByLabelText('Loading skeleton')).toBeTruthy();
    expect(screen.queryByText('Actual Content')).toBeNull();
  });

  it('renders error state with retry button on error', () => {
    const onRetry = vi.fn();
    render(
      <DataState error="Connection closed" onRetry={onRetry}>
        <div>Actual Content</div>
      </DataState>
    );
    expect(screen.getByText('Node Disconnected')).toBeTruthy();
    expect(screen.queryByText('Actual Content')).toBeNull();

    const retryBtn = screen.getByText('Retry');
    fireEvent.click(retryBtn);
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it('renders empty state with CTA button', () => {
    const onAction = vi.fn();
    render(
      <DataState
        empty={true}
        emptyProps={{
          title: 'No files yet',
          description: 'Upload your first file.',
          actionLabel: '+ Upload',
          onAction,
        }}
      >
        <div>Actual Content</div>
      </DataState>
    );
    expect(screen.getByText('No files yet')).toBeTruthy();
    expect(screen.getByText('Upload your first file.')).toBeTruthy();

    const actionBtn = screen.getByText('+ Upload');
    fireEvent.click(actionBtn);
    expect(onAction).toHaveBeenCalledTimes(1);
  });

  it('renders children when not loading, no error, and not empty', () => {
    render(
      <DataState loading={false} error="" empty={false}>
        <div>Actual Content</div>
      </DataState>
    );
    expect(screen.getByText('Actual Content')).toBeTruthy();
  });
});
