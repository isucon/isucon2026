import React from "react";

interface Props {
  serverId: string;
}

interface State {
  inviteURL: string;
  loading: boolean;
}

export class DiscordJoinGuide extends React.Component<Props, State> {
  private unmounted = false;

  constructor(props: Props) {
    super(props);
    const inviteURL = document.querySelector<HTMLMetaElement>('meta[name="isux:discord-invite-url"]')?.content || "";
    this.state = { inviteURL, loading: !inviteURL };
  }

  async componentDidMount() {
    if (this.state.inviteURL) return;
    let inviteURL = "";
    try {
      const response = await fetch(`https://discord.com/api/guilds/${this.props.serverId}/widget.json`);
      if (response.ok) {
        const widget = await response.json();
        if (typeof widget.instant_invite === "string") inviteURL = widget.instant_invite;
      }
    } catch {
      // Network errors use the same explicit fallback as a disabled invite.
    }
    if (!this.unmounted) this.setState({ inviteURL, loading: false });
  }

  componentWillUnmount() {
    this.unmounted = true;
  }

  render() {
    return (
      <div className="mb-3">
        <p>連携だけではDiscordサーバーに参加しません。連携したDiscordアカウントで、招待リンクから参加してください。</p>
        {this.state.inviteURL ? (
          <a href={this.state.inviteURL} target="_blank" rel="noreferrer">
            Discordサーバーに参加
          </a>
        ) : this.state.loading ? (
          <p>招待リンクを取得しています。</p>
        ) : (
          <p>招待リンクを取得できませんでした。運営にお問い合わせください。</p>
        )}
      </div>
    );
  }
}
