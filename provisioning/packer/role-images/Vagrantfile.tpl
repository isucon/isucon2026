{{ .DefaultTemplate }}

# Give package installation and artifact extraction enough headroom while
# producing role boxes. This affects only Packer's temporary build guest.
Vagrant.configure("2") do |config|
  config.vm.provider "virtualbox" do |vb|
    vb.cpus = 4
    vb.memory = 4096
  end
end
