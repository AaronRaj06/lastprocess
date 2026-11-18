# Sourced by the azure-*.sh scripts. Refuses to touch anything but the student subscription.
RG=${RG:-lastprocess-rg}
VM=${VM:-lastprocess-vm}
sub=$(az account show --query name -o tsv) || exit 1
if [ "$sub" != "Azure for Students" ]; then
  echo "Refusing: active subscription is '$sub', not 'Azure for Students'." >&2
  exit 1
fi
