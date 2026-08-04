import HomeScreen from '../screens/home';
import ProfileScreen from '../screens/profile';

const Stack = createNativeStackNavigator();

export function Root() {
  return (
    <Stack.Navigator>
      <Stack.Screen name="Home" component={HomeScreen} />
      <Stack.Screen name="Profile" component={ProfileScreen} />
    </Stack.Navigator>
  );
}
