# frozen_string_literal: true

module Isureal
  class App
    def initialize_handler
      system('../sql/init.sh', exception: true)
      json_response(200, { language: 'ruby' })
    end
  end
end
