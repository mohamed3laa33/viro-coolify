import type { MetadataRoute } from "next";

// PWA web app manifest. theme_color matches --background (#0f0f12) and the
// brand violet drives icon tinting. The generated app/icon route supplies the
// purpose-any icon.
export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Vortex — Deploy apps close to your users",
    short_name: "Vortex",
    description:
      "Vortex is a global application platform. Ship containers to the edge, scale instantly, and run managed databases with zero-config TLS.",
    start_url: "/",
    display: "standalone",
    background_color: "#0f0f12",
    theme_color: "#0f0f12",
    icons: [
      {
        src: "/icon",
        sizes: "32x32",
        type: "image/png",
      },
    ],
  };
}
