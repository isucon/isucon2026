import React from "react";

type Level = "normal" | "warning" | "danger";

export const AdminAttentionSound: React.FC<{ level: Level }> = ({ level }) => {
  const context = React.useRef<AudioContext | null>(null);
  const mounted = React.useRef(true);
  const [enabled, setEnabled] = React.useState(false);
  const [starting, setStarting] = React.useState(false);
  const [error, setError] = React.useState(false);

  React.useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      if (context.current) {
        context.current.onstatechange = null;
        void context.current.close().catch(() => {});
        context.current = null;
      }
    };
  }, []);

  const toggle = async () => {
    if (enabled) {
      setEnabled(false);
      return;
    }
    setStarting(true);
    setError(false);
    try {
      // AudioContext creation/resume must happen in the button's user gesture.
      if (!context.current) {
        const audio = new window.AudioContext();
        context.current = audio;
        audio.onstatechange = () => {
          if (mounted.current && audio.state !== "running") setEnabled(false);
        };
      }
      await context.current.resume();
      if (mounted.current) {
        if (context.current?.state !== "running") throw new Error("audio suspended");
        setEnabled(true);
      }
    } catch {
      if (mounted.current) setError(true);
    } finally {
      if (mounted.current) setStarting(false);
    }
  };

  React.useEffect(() => {
    const audio = context.current;
    if (!enabled || level === "normal" || !audio) return;
    const active = new Map<OscillatorNode, GainNode>();
    const play = () => {
      if (audio.state !== "running") return;
      for (let i = 0; i < (level === "danger" ? 2 : 1); i++) {
        const at = audio.currentTime + i * 0.25;
        const oscillator = audio.createOscillator();
        const gain = audio.createGain();
        oscillator.type = "sine";
        oscillator.frequency.value = level === "danger" ? 880 : 660;
        gain.gain.setValueAtTime(0, at);
        gain.gain.linearRampToValueAtTime(0.06, at + 0.015);
        gain.gain.linearRampToValueAtTime(0, at + 0.18);
        oscillator.connect(gain);
        gain.connect(audio.destination);
        active.set(oscillator, gain);
        oscillator.onended = () => {
          oscillator.disconnect();
          gain.disconnect();
          active.delete(oscillator);
        };
        oscillator.start(at);
        oscillator.stop(at + 0.2);
      }
    };
    play();
    const timer = window.setInterval(play, 30_000);
    return () => {
      window.clearInterval(timer);
      for (const [oscillator, gain] of active) {
        oscillator.onended = null;
        oscillator.stop();
        oscillator.disconnect();
        gain.disconnect();
      }
      active.clear();
    };
  }, [enabled, level]);

  return (
    <>
      <button type="button" className="button is-small" aria-pressed={enabled} disabled={starting} onClick={toggle}>
        {starting ? "通知音を準備中…" : enabled ? "通知音を無効にする" : "通知音を有効にする"}
      </button>
      {error && <span role="status">通知音を再生できませんでした。もう一度有効にしてください。</span>}
    </>
  );
};
