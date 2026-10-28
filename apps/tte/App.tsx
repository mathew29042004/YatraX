import { StatusBar } from "expo-status-bar";
import { ActivityIndicator, View } from "react-native";
import { Run } from "./src/screens/Run";
import { Duties, Login } from "./src/screens/Start";
import { StoreProvider, useStore } from "./src/store";
import { color } from "./src/ui";

function Root() {
  const { ready, session, run } = useStore();
  if (!ready) {
    return (
      <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
        <ActivityIndicator color={color.accent} />
      </View>
    );
  }
  // A downloaded run keeps working even if the session lapsed offline; sync asks to sign in again.
  if (run) return <Run />;
  if (!session) return <Login />;
  return <Duties />;
}

export default function App() {
  return (
    <StoreProvider>
      <View style={{ flex: 1, backgroundColor: color.canvas }}>
        <Root />
        <StatusBar style="dark" />
      </View>
    </StoreProvider>
  );
}
