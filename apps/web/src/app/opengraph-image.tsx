import { ImageResponse } from "next/og";

// Branded Open Graph / Twitter card. Colors mirror the design tokens in
// globals.css as literal hex because ImageResponse cannot resolve CSS vars.
export const alt = "Vortex — Deploy apps close to your users";
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

export default function OpengraphImage() {
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
          padding: "80px",
          background: "#0f0f12",
          backgroundImage:
            "radial-gradient(circle at 75% 15%, rgba(157,78,221,0.28), transparent 55%)",
          color: "#f4f3f7",
          fontFamily: "sans-serif",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 20 }}>
          <svg
            width="72"
            height="72"
            viewBox="0 0 32 32"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
          >
            <defs>
              <linearGradient
                id="og-balloon"
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
              fill="url(#og-balloon)"
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
          <span style={{ fontSize: 44, fontWeight: 600, letterSpacing: -1 }}>
            Vortex
          </span>
        </div>
        <div
          style={{
            marginTop: 48,
            fontSize: 76,
            fontWeight: 700,
            lineHeight: 1.05,
            letterSpacing: -2,
            maxWidth: 900,
          }}
        >
          Deploy apps close to your users.
        </div>
        <div
          style={{
            marginTop: 28,
            fontSize: 32,
            color: "#a1a0ac",
            maxWidth: 880,
          }}
        >
          A global application platform. Ship containers to the edge, scale
          instantly, and run managed databases with zero-config TLS.
        </div>
      </div>
    ),
    size,
  );
}
