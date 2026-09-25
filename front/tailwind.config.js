/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
  theme: {
    extend: {
      colors: {
        // 品牌主色：indigo（蓝紫）。深色底上比 Tailwind 默认 blue 更精致、
        // 更有 P2P/网络产品的科技感；cyan 留作次级强调（如在线态）。
        brand: {
          50: '#eef2ff', 100: '#e0e7ff', 200: '#c7d2fe', 300: '#a5b4fc',
          400: '#818cf8', 500: '#6366f1', 600: '#4f46e5', 700: '#4338ca',
          800: '#3730a3', 900: '#312e81', 950: '#1e1b4b',
        },
        // 表面层级：页面底 → 卡片 → 抬升（输入/悬浮面板）→ hover → 描边。
        // 比裸 zinc 系列多一层「近黑偏蓝」的底，玻璃质感统一。
        surface: {
          DEFAULT: '#101014',      // 页面底
          card: '#191920',         // 卡片
          raised: '#21212a',       // 输入框/悬浮面板
          hover: '#2a2a34',        // hover 态
          border: '#2b2b34',       // 描边
        },
      },
      fontFamily: {
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Consolas', 'monospace'],
      },
      boxShadow: {
        card: 'inset 0 1px 0 0 rgb(255 255 255 / 0.04), 0 10px 28px -14px rgb(0 0 0 / 0.55)',
        pop: 'inset 0 1px 0 0 rgb(255 255 255 / 0.06), 0 18px 44px -14px rgb(0 0 0 / 0.65)',
        glow: '0 0 0 1px rgb(99 102 241 / 0.35), 0 10px 34px -10px rgb(99 102 241 / 0.4)',
      },
      borderRadius: {
        card: '14px',
      },
    },
  },
  plugins: [],
}