import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import Display from '../src/features/display/pages/Display';

const { sendFrameMock, onMessageMock, onStatusMock, adminMock } = vi.hoisted(() => ({
  sendFrameMock: vi.fn(),
  onMessageMock: vi.fn(),
  onStatusMock: vi.fn(),
  adminMock: vi.fn(),
}));

vi.mock('../src/ws.js', () => ({
  getStatus: () => 'open',
  onStatus: onStatusMock,
  onMessage: onMessageMock,
  sendFrame: sendFrameMock,
  admin: adminMock,
}));

vi.mock('../src/platform/transport-ws', () => ({
  getStatus: () => 'open',
  onStatus: onStatusMock,
  onMessage: onMessageMock,
  sendFrame: sendFrameMock,
  admin: adminMock,
}));

describe('Display.jsx page (Issue #243)', () => {
  let messageHandler = null;

  beforeEach(() => {
    sendFrameMock.mockReset();
    onMessageMock.mockReset();
    onStatusMock.mockReset();
    adminMock.mockReset();

    adminMock.mockResolvedValue({});

    onStatusMock.mockImplementation((cb) => {
      cb('open');
      return () => {};
    });

    onMessageMock.mockImplementation((cb) => {
      messageHandler = cb;
      return () => {
        messageHandler = null;
      };
    });
  });

  it('renders standby state and registers display screen on mount', () => {
    render(
      <MemoryRouter>
        <Display />
      </MemoryRouter>
    );

    // Screen should render standby instructions
    expect(screen.getByText(/受控展示大屏已上线/i)).toBeInTheDocument();
    expect(screen.getByText(/屏幕标识:/i)).toBeInTheDocument();

    // Verify registration frame was sent
    expect(sendFrameMock).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'display',
        action: 'register',
      })
    );
  });

  it('renders image when receiving display show frame', () => {
    render(
      <MemoryRouter>
        <Display />
      </MemoryRouter>
    );

    // Simulate incoming show frame
    act(() => {
      if (messageHandler) {
        messageHandler({
          type: 'display',
          action: 'show',
          mediaType: 'image',
          hash: '6a0f8b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a',
          title: 'Sunset_Photo.jpg',
        });
      }
    });

    const img = screen.getByRole('img');
    expect(img).toBeInTheDocument();
    expect(img).toHaveAttribute('src', '/files/download/6a0f8b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a');
    expect(screen.getByText('Sunset_Photo.jpg')).toBeInTheDocument();
  });

  it('clears displayed content on clear frame', () => {
    render(
      <MemoryRouter>
        <Display />
      </MemoryRouter>
    );

    // Show image
    act(() => {
      if (messageHandler) {
        messageHandler({
          type: 'display',
          action: 'show',
          mediaType: 'image',
          hash: 'abc123',
          title: 'test.png',
        });
      }
    });
    expect(screen.getByRole('img')).toBeInTheDocument();

    // Clear frame
    act(() => {
      if (messageHandler) {
        messageHandler({
          type: 'display',
          action: 'clear',
        });
      }
    });
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    expect(screen.getByText(/受控展示大屏已上线/i)).toBeInTheDocument();
  });

  it('unregisters screen on unmount', () => {
    const { unmount } = render(
      <MemoryRouter>
        <Display />
      </MemoryRouter>
    );

    unmount();

    expect(sendFrameMock).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'display',
        action: 'unregister',
      })
    );
  });

  it('receives stream chunk frames and renders media with LIVE badge (Issue #244)', () => {
    render(
      <MemoryRouter>
        <Display />
      </MemoryRouter>
    );

    act(() => {
      if (messageHandler) {
        messageHandler({
          type: 'stream',
          action: 'chunk',
          streamId: 'live-camera',
          chunk: {
            seq: 42,
            hash: 'stream-hash-42',
            mimeType: 'image/jpeg',
            title: 'Cam Feed',
          },
        });
      }
    });

    const img = screen.getByRole('img');
    expect(img).toBeInTheDocument();
    expect(img).toHaveAttribute('src', '/files/download/stream-hash-42');
    expect(screen.getByText(/LIVE #42/i)).toBeInTheDocument();
  });
});
