/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{js,ts,jsx,tsx}"],
  theme: {
    extend: {
      colors: {
        // Brand primary color: indigo (blue-purple). On dark backgrounds, it looks more refined than Tailwind's default blue,
        // giving it a more tech-y P2P/network product feel; cyan is reserved as a secondary accent (e.g. online status).
        brand: {
          50: '#eef2ff', 100: '#e0e7ff', 200: '#c7d2fe', 300: '#a5b4fc',
          400: '#818cf8', 500: '#6366f1', 600: '#4f46e5', 700: '#4338ca',
          800: '#3730a3', 900: '#312e81', 950: '#1e1b4b',
        },
        // Surface hierarchy: page background → card → raised (input/floating panel) → hover → border.
        // One extra "near-black with a blue tint" base layer beyond plain zinc, keeping the glass aesthetic consistent.
        surface: {
          DEFAULT: '#101014',      // Page background
          card: '#191920',         // Card
          raised: '#21212a',       // Input / floating panel
          hover: '#2a2a34',        // Hover state
          border: '#2b2b34',       // Border
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
