import { ApiClient } from "./ApiClient";
import React from "react";
import type { GetCurrentSessionResponse } from "../../proto/isuxportal/services/common/me_pb";
import { Contest_Status } from "../../proto/isuxportal/resources/contest_pb";

export interface Props {
  session: GetCurrentSessionResponse;
  client: ApiClient;
}

export interface State {}

export class Landing extends React.Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = {};
  }

  public render() {
    return (
      <>
        {this.renderHero()}
        {this.renderMain()}
      </>
    );
  }

  public renderHero() {
    return (
      <header className="hero">
        <div className="hero-body">
          <div className="isux-landing-logo-wrapper container has-text-centered">
            <img src="/isucon2026-logo.svg" alt="ISUCON2026" />
            <p>
              開催日：<time dateTime="2026-10-31">2026年10月31日（土）</time>
            </p>
          </div>
        </div>
      </header>
    );
  }

  public renderMain() {
    if (this.props.session.contestant) {
      return (
        <main>
          <p className="block">参加登録が完了しています。情報を修正したい場合は右上の「参加登録/修正」から行えます。</p>
        </main>
      );
    }

    if (this.props.session.contest?.status === Contest_Status.REGISTRATION) {
      return (
        <main>
          <p className="block">参加登録は右上から行えます</p>
        </main>
      );
    } else {
      return (
        <main>
          <p className="block">現在は参加登録を受け付けていません</p>
        </main>
      );
    }
  }
}
