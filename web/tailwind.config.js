/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        bg: "#0b0f14",
        panel: "#11161d",
        edge: "#1f2937",
        mut: "#8b98a9",
        ok: "#3fb950",
        warn: "#d29922",
        crit: "#f85149",
        info: "#58a6ff",
      },
      fontFamily: {
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "Consolas", "monospace"],
      },
    },
  },
  plugins: [],
};
