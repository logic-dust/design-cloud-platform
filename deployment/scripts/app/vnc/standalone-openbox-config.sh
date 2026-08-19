echo '
<?xml version="1.0" encoding="UTF-8"?>

<openbox_menu xmlns="http://openbox.org/3.4/menu">

<menu id="root-menu" label="Openbox 3">
  <separator label="Openbox"/>
  <item label="Exit">
    <action name="Exit">
      <prompt>yes</prompt>
    </action>
  </item>
  <separator/>
  <item label="Log Out">
    <action name="SessionLogout">
      <prompt>yes</prompt>
    </action>
  </item>
</menu>

</openbox_menu>
' >/etc/xdg/openbox/menu.xml
