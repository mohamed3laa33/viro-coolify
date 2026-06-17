import Link from "next/link";
import { Logo } from "@/components/logo";
import { GuestGuard } from "@/components/guest-guard";

export default function AuthLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <GuestGuard>
      <div className="relative flex min-h-screen flex-col items-center justify-center overflow-hidden px-4 py-12">
        <div className="absolute inset-0 grid-bg opacity-20" aria-hidden />
        <div
          className="pointer-events-none absolute left-1/2 top-[-12rem] h-[28rem] w-[28rem] -translate-x-1/2 rounded-full bg-brand-balloon opacity-20 blur-[120px]"
          aria-hidden
        />
        <Link href="/" className="relative z-10 mb-8">
          <Logo size={40} withWordmark />
        </Link>
        <div className="relative z-10 w-full max-w-md">{children}</div>
      </div>
    </GuestGuard>
  );
}
