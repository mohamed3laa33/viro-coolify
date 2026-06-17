"use client";

import { useEffect } from "react";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";

export default function DashboardError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error(error);
  }, [error]);

  return (
    <div className="flex min-h-[60vh] items-center justify-center">
      <Card className="w-full max-w-md" role="alert">
        <CardHeader>
          <CardTitle>Something went wrong</CardTitle>
          <CardDescription>
            We hit an unexpected error while loading this page. You can try
            again, and if the problem persists, reach out to support.
          </CardDescription>
        </CardHeader>
        {error.digest && (
          <CardContent>
            <p className="font-mono text-xs text-muted-foreground">
              Error reference: {error.digest}
            </p>
          </CardContent>
        )}
        <CardFooter>
          <Button onClick={() => reset()}>Retry</Button>
        </CardFooter>
      </Card>
    </div>
  );
}
