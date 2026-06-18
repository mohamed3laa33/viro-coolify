import { ImageResponse } from "next/og";

// Generated favicon: the violet brand mark on a near-black rounded square.
// Colors mirror the design tokens in globals.css (--brand-violet / magenta /
// deep violet) as literal hex because ImageResponse cannot resolve CSS vars.
export const size = { width: 32, height: 32 };
export const contentType = "image/png";

export default function Icon() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "#0f0f12",
          borderRadius: 7,
        }}
      >
        <svg
          width="24"
          height="24"
          viewBox="0 0 32 32"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <defs>
            <linearGradient
              id="balloon"
              x1="4"
              y1="2"
              x2="28"
              y2="26"
              gradientUnits="userSpaceOnUse"
            >
              <stop stopColor="#9D4EDD" />
              <stop offset="1" stopColor="#E0218A" />
            </linearGradient>
          </defs>
          <path
            d="M16 2C9.92 2 5 6.7 5 12.5c0 4.9 3.2 8.2 6.5 10.1.7.4 1.1 1.2 1.1 2v.4h6.8v-.4c0-.8.4-1.6 1.1-2C23.8 20.7 27 17.4 27 12.5 27 6.7 22.08 2 16 2Z"
            fill="url(#balloon)"
          />
          <rect
            x="13.7"
            y="26.4"
            width="4.6"
            height="3.6"
            rx="1"
            fill="#5B21B6"
          />
        </svg>
      </div>
    ),
    size,
  );
}
