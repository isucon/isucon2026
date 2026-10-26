# frozen_string_literal: true

require 'pure_jpeg'

module Isureal
  module JPEG
    Image = Data.define(:width, :height)

    module_function

    def decode(data)
      image = PureJPEG::Decoder.new(data).decode
      Image.new(width: image.width, height: image.height)
    rescue StandardError
      nil
    end
  end
end
