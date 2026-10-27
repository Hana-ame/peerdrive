import React from 'react';

/**
 * ErrorBoundary — 路由级错误边界（审计 ISSUE-04 / 外部版补丁遗漏组件，2026-10-15 补全）。
 *
 * 定位：包裹 <Suspense><Routes>（App.jsx）。任一子页面渲染/生命周期抛出未捕获
 * 异常时，React 默认行为是卸载整棵组件树 → 白屏。本边界把错误收敛成一屏可恢复
 * 的错误卡片；顶栏导航（ErrorBoundary 外层）保持可用，用户可刷新或返回导航。
 */
export default class ErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { hasError: false, error: null };
  }

  static getDerivedStateFromError(error) {
    return { hasError: true, error };
  }

  componentDidCatch(error, info) {
    // 留痕到控制台，便于排查；不对外暴露堆栈细节。
    console.error('[ErrorBoundary] render error caught:', error, info?.componentStack ?? '');
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null });
    if (typeof this.props.onReset === 'function') {
      try {
        this.props.onReset();
      } catch (e) {
        console.error('[ErrorBoundary] onReset error:', e);
      }
    }
  };

  render() {
    if (this.state.hasError) {
      if (typeof this.props.fallback === 'function') {
        return this.props.fallback({ error: this.state.error, resetError: this.handleReset });
      }
      return (
        <div className="p-8 h-full flex items-center justify-center">
          <div className="max-w-md w-full rounded-2xl border border-red-400/20 bg-white/[0.03] p-8 text-center">
            <div className="text-3xl mb-3" aria-hidden="true">⚠️</div>
            <h2 className="text-lg font-semibold text-white mb-2">
              页面渲染出错了
              <span className="ml-2 text-sm font-normal text-gray-400">Something went wrong</span>
            </h2>
            <p className="text-sm text-gray-400 mb-6">
              此页面遇到未捕获异常。导航仍可用，可尝试局部重试或刷新页面。
            </p>
            <div className="flex items-center justify-center gap-3">
              <button
                type="button"
                onClick={this.handleReset}
                className="px-4 py-2 rounded-lg bg-indigo-500 hover:bg-indigo-400 text-white text-sm font-medium transition-colors focus:outline-none"
              >
                重试 · Try Again
              </button>
              <button
                type="button"
                onClick={() => window.location.reload()}
                className="px-4 py-2 rounded-lg bg-white/[0.08] hover:bg-white/[0.12] text-gray-200 text-sm font-medium transition-colors focus:outline-none"
              >
                刷新页面 · Reload
              </button>
            </div>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}