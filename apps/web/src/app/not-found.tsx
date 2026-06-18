import Link from "next/link";
import {
  Card,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Page not found</CardTitle>
          <CardDescription>
            The page you&apos;re looking for doesn&apos;t exist or may have been
            moved.
          </CardDescription>
        </CardHeader>
        <CardFooter>
          <Link href="/dashboard">
            <Button>Back to dashboard</Button>
          </Link>
        </CardFooter>
      </Card>
    </div>
  );
}
