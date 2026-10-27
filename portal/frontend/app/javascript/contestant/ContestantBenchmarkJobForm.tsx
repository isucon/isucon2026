import { ApiClient } from "../ApiClient";

import React from "react";
import { Navigate } from "react-router-dom";

import { ErrorMessage } from "../ErrorMessage";
import { create } from "@bufbuild/protobuf";
import { EnqueueBenchmarkJobRequestSchema } from "../../../proto/isuxportal/services/contestant/benchmark_pb";
import type { GetCurrentSessionResponse } from "../../../proto/isuxportal/services/common/me_pb";

type Props = {
  session: GetCurrentSessionResponse;
  client: ApiClient;
};

export const ContestantBenchmarkJobForm: React.FC<Props> = (props: Props) => {
  const [redirect, setRedirect] = React.useState<JSX.Element | null>(null);
  const [requesting, setRequesting] = React.useState<boolean>(false);
  const [error, setError] = React.useState<Error | null>(null);
  const targetId = props.session.contestantInstances[0]?.id;
  const onSubmit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (requesting || !targetId) return;

    try {
      setRequesting(true);
      const resp = await props.client.enqueueBenchmarkJob(
        create(EnqueueBenchmarkJobRequestSchema, {
          targetId,
        }),
      );
      setRedirect(
        <Navigate
          to={{
            pathname: `/contestant/benchmark_jobs/${encodeURIComponent(resp.job!.id!.toString())}`,
          }}
        />,
      );
    } catch (e) {
      setError(e);
      setRequesting(false);
    }
  };

  return (
    <div className="card mt-5">
      {redirect}
      <div className="card-content">
        <form onSubmit={onSubmit}>
          <div className="field has-addons">
            <div className="control">
              <button className="button is-primary" type="submit" disabled={requesting || !targetId}>
                Enqueue
              </button>
            </div>
          </div>
          <p className="is-size-7">Enqueue を押すと、固定のベンチ対象に対して計測を開始します。</p>
          {error ? <ErrorMessage error={error} /> : null}
        </form>
      </div>
    </div>
  );
};
