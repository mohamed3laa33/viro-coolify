import { cn } from "@/lib/utils";

export interface LogoProps {
  className?: string;
  size?: number;
  withWordmark?: boolean;
}

/**
 * Vortex brand mark — a hot-air balloon with a violet→magenta gradient,
 * a nod to fly.io's balloon iconography.
 */
export function Logo({ className, size = 28, withWordmark = false }: LogoProps) {
  return (
    <span className={cn("inline-flex items-center gap-2", className)}>
      <svg
        width={size}
        height={size}
        viewBox="0 0 32 32"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden="true"
      >
        <defs>
          <linearGradient
            id="viro-balloon"
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
        {/* Balloon envelope */}
        <path
          d="M16 2C9.92 2 5 6.7 5 12.5c0 4.9 3.2 8.2 6.5 10.1.7.4 1.1 1.2 1.1 2v.4h6.8v-.4c0-.8.4-1.6 1.1-2C23.8 20.7 27 17.4 27 12.5 27 6.7 22.08 2 16 2Z"
          fill="url(#viro-balloon)"
        />
        {/* Envelope seams */}
        <path
          d="M16 2c-2.4 2.9-3.7 6.6-3.7 10.5 0 4 1.3 7.7 3.7 10.6 2.4-2.9 3.7-6.6 3.7-10.6C19.7 8.6 18.4 4.9 16 2Z"
          fill="#ffffff"
          fillOpacity="0.18"
        />
        {/* Basket */}
        <rect x="13.7" y="26.4" width="4.6" height="3.6" rx="1" fill="#5B21B6" />
        <path
          d="M13.8 25.8 13 27m6.2-1.2L20 27"
          stroke="#5B21B6"
          strokeWidth="1"
          strokeLinecap="round"
        />
      </svg>
      {withWordmark && (
        <span className="text-lg font-semibold tracking-tight text-foreground">
          Vortex
        </span>
      )}
    </span>
  );
}
