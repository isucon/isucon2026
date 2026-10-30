# RSpec 実行用イメージ。ホストの Ruby に依存しないための箱。
FROM mirror.gcr.io/library/ruby:3.3
RUN apt-get update && apt-get install -y --no-install-recommends libpq-dev build-essential && rm -rf /var/lib/apt/lists/*
WORKDIR /work
ENV BUNDLE_PATH=/work/.bundle
