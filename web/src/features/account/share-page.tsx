import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";
import { ErrorState, Loading } from "@/components/ui/states";
import { getShareOptions, joinShareMutation } from "@/lib/api/@tanstack/react-query.gen";
import { problemMessage } from "@/lib/problem";
import { AuthCard } from "./auth-card";

// SharePage lets a guest in with a display name (REQ-086).
export function SharePage({ token }: { token: string }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const share = useQuery({ ...getShareOptions({ path: { token } }), retry: false });
  const [name, setName] = useState("");
  const join = useMutation({
    ...joinShareMutation(),
    onSuccess: async (info) => {
      await qc.invalidateQueries();
      navigate({ to: "/bundles/$bundleId", params: { bundleId: info.bundle_id } });
    },
  });
  if (share.isPending) return <Loading label="Opening the link" />;
  if (share.isError) {
    return (
      <AuthCard title="This link does not work">
        <ErrorState message={problemMessage(share.error)} />
      </AuthCard>
    );
  }
  return (
    <AuthCard title={share.data.title} lead="You are invited to read this spec. Enter your name.">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          join.mutate({ path: { token }, body: { display_name: name.trim() } });
        }}
        className="space-y-4"
      >
        <div>
          <Label htmlFor="name">Your name</Label>
          <Input id="name" maxLength={60} value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </div>
        {join.isError ? <ErrorState message={problemMessage(join.error)} /> : null}
        <Button
          type="submit"
          variant="primary"
          className="w-full justify-center"
          disabled={join.isPending || !name.trim()}
        >
          {join.isPending ? "Opening" : "Open the spec"}
        </Button>
      </form>
    </AuthCard>
  );
}
