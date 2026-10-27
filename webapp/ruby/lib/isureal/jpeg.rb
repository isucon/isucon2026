# frozen_string_literal: true

require 'vips'

Vips.concurrency_set(1)

module Isureal
  module JPEG
    Image = Data.define(:width, :height)

    module_function

    def decode(data)
      image = Vips::Image.jpegload_buffer(data, access: :sequential, fail_on: :error)
      image.avg
      Image.new(width: image.width, height: image.height)
    rescue StandardError
      nil
    end
  end
end
