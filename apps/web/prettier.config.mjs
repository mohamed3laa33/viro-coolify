// Prettier configuration for the Vortex web app. Mirrors the existing code
// style across the repo: 2-space indentation, double quotes, semicolons,
// trailing commas, and an 80-column print width.
/** @type {import("prettier").Config} */
const config = {
  printWidth: 80,
  tabWidth: 2,
  useTabs: false,
  semi: true,
  singleQuote: false,
  trailingComma: "all",
  bracketSpacing: true,
  arrowParens: "always",
  endOfLine: "lf",
};

export default config;
